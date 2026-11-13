package tui

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/inspect"
)

// reportMode selects which of the two reporting screens a reportWiz backs. They
// show the same measured carrier: the CLI's `score` and `capacity` print nearly
// the same fields, and the design sheet draws one "SCORE / CAPACITY" screen.
type reportMode int

const (
	modeScore reportMode = iota
	modeCapacity
)

// label is the name the header and hints use. It is capitalised like the menu
// entry, so the screen names itself the way the user found it.
func (mode reportMode) label() string {
	if mode == modeCapacity {
		return "Capacity"
	}
	return "Score"
}

// reportStep is one screen of a report flow.
type reportStep int

const (
	reportPick reportStep = iota
	reportShow
)

// reportWiz is the state of the Score and Capacity screens. Both are
// presentation-only: they decode a carrier, measure it, and show the numbers the
// CLI prints. Nothing is written and no passphrase is involved.
type reportWiz struct {
	mode reportMode
	step reportStep
	pick picker

	path    string
	info    *inspect.Info
	loading bool
	err     error
}

func newReportWiz(mode reportMode) reportWiz {
	return reportWiz{mode: mode, pick: newPicker(workingDir(), pngOnly)}
}

func (w *reportWiz) reset(mode reportMode) { *w = newReportWiz(mode) }

// describeMsg carries a measured carrier back to the UI goroutine.
type describeMsg struct {
	seq  int
	info *inspect.Info
	err  error
}

// describeCmd decodes a carrier and measures it off the UI goroutine. The cover
// score is an O(pixels) pass after an O(pixels) decode, too slow for Update; the
// screen shows a busy row while it runs.
//
// The score is always measured, including for a transparent carrier: the TUI's
// report is the merged score/capacity view, and a verdict is still the most
// useful thing to say about a carrier the engine will refuse.
func describeCmd(seq int, path string) tea.Cmd {
	return func() tea.Msg {
		car, err := carrier.Open(path)
		if err != nil {
			return describeMsg{seq: seq, err: err}
		}
		info := inspect.Describe(car, true)
		return describeMsg{seq: seq, info: &info}
	}
}

// applyDescribe stores a finished measurement. The screen decides which flow
// asked for it, so one message type serves the report and the inspector.
func (m Model) applyDescribe(msg describeMsg) Model {
	if msg.seq != m.gen {
		return m
	}
	switch m.screen {
	case screenScore, screenCapacity:
		m.report.info = msg.info
		m.report.err = msg.err
		m.report.loading = false
	case screenInspect:
		m.inspector.info = msg.info
		m.inspector.err = msg.err
		m.inspector.loading = false
	}
	return m
}

// loadReport starts the measurement for path and shows the busy row.
func (m Model) loadReport(path string) (Model, tea.Cmd) {
	m.gen++
	w := &m.report
	w.step = reportShow
	w.path = path
	w.info = nil
	w.err = nil
	w.loading = true
	return m, describeCmd(m.gen, path)
}

// handleReportKey dispatches a key on the Score and Capacity screens.
func (m Model) handleReportKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.report.step == reportPick {
		return m.reportPickKey(k)
	}
	// The report has no fields. Enter and Esc both go back to the listing; every
	// other key falls through so q and ? keep working.
	switch {
	case keySelect(k), keyBack(k):
		m.report.step = reportPick
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) reportPickKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.report
	if w.pick.typing {
		value, action := editText(w.pick.manual, k)
		w.pick.manual = value
		switch action {
		case editChanged:
			w.pick.note = ""
			return m, nil, true
		case editSubmit:
			if path, ok := w.pick.appendManual(acceptCarrier); ok {
				// The path was accepted, so the field has done its job and the
				// listing takes the keys back.
				w.pick.typing = false
				next, cmd := m.loadReport(path)
				return next, cmd, true
			}
			return m, nil, true
		case editTab, editEsc:
			w.pick.typing = false
			return m, nil, true
		default:
			return m, nil, true
		}
	}

	switch {
	case keyUp(k):
		w.pick.move(-1)
	case keyDown(k):
		w.pick.move(1)
	case keyRight(k):
		w.pick.descend()
	case keyLeft(k):
		w.pick.ascend()
	case keyTab(k):
		w.pick.typing = true
	case keySelect(k):
		entry, ok := w.pick.current()
		if !ok {
			break
		}
		if entry.dir {
			w.pick.descend()
			break
		}
		next, cmd := m.loadReport(filepath.Join(w.pick.dir, entry.name))
		return next, cmd, true
	case keyBack(k):
		return m.leaveFlow(), nil, true
	default:
		return m, nil, false
	}
	return m, nil, true
}

// reportRows renders the Score and Capacity screens.
func (m Model) reportRows(inner, maxRows int) []string {
	if m.report.step == reportPick {
		return fitRows(m.reportPickRows(inner, maxRows), maxRows)
	}
	return fitRows(m.reportShowRows(inner), maxRows)
}

func (m Model) reportPickRows(inner, maxRows int) []string {
	w := &m.report
	rows := m.flowHeader(w.mode.label(), "choose a carrier", inner)

	room := maxRows - len(rows) - 5
	if room < 3 {
		room = 3
	}
	rows = append(rows, m.pickerRows(w.pick, inner, room)...)
	rows = append(rows, "")
	rows = append(rows, fieldLine("path", w.pick.manual, w.pick.typing, inner))
	if w.err != nil {
		rows = append(rows, failureRow(w.err, inner))
	}
	if w.pick.note != "" {
		rows = append(rows, noteRow(clip(w.pick.note, inner)))
	}
	return rows
}

func (m Model) reportShowRows(inner int) []string {
	w := &m.report
	rows := m.flowHeader(w.mode.label(), clip(filepath.Base(w.path), 40), inner)

	if w.loading {
		rows = append(rows, spinner(m.frame)+styleDim.Render(" reading the carrier and scoring the cover…"))
		return rows
	}
	if w.err != nil {
		rows = append(rows, failureRow(w.err, inner), "")
		rows = append(rows, styleDim.Render("enter or esc returns to the carrier list"))
		return rows
	}
	if w.info == nil {
		return rows
	}

	info := w.info
	rows = append(rows, row2("dimensions", fmt.Sprintf("%dx%d", info.Width, info.Height), inner))
	rows = append(rows, row2("channels", fmt.Sprintf("%d", info.Channels), inner))
	if info.Alpha {
		rows = append(rows, row2("alpha", styleWarn.Render("yes - transparent carriers cannot be used"), inner))
	} else {
		rows = append(rows, row2("alpha", "no", inner))
	}
	rows = append(rows, row2("colour chunks", chunkLabel(info.Chunks), inner))
	rows = append(rows, "")

	rows = append(rows, row2(fmt.Sprintf("textured_fraction %.3f", info.Score.TexturedFraction),
		verdictStyle(info.Score.Verdict).Render(info.Score.Verdict.String()), inner))
	rows = append(rows, row2("median_rho", fmt.Sprintf("%.6g", info.Score.MedianRho), inner))
	rows = append(rows, "")

	rows = append(rows, styleDim.Render("Capacity"))
	rows = append(rows, row2("raw", fmt.Sprintf("%d bytes", info.Raw), inner))
	rows = append(rows, row2("usable", fmt.Sprintf("%d bytes", info.Usable), inner))
	rows = append(rows, row2("suggested (10%)", fmt.Sprintf("%d bytes", info.Suggested), inner))
	rows = append(rows, row2("strong (5%)", fmt.Sprintf("%d bytes", info.Strong), inner))

	for i, msg := range info.Warnings {
		if i == 3 {
			rows = append(rows, styleWarn.Render(fmt.Sprintf("  +%d more warning", len(info.Warnings)-i)))
			break
		}
		rows = append(rows, noteRow(clip(msg, inner-2)))
	}

	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Nothing is written. No passphrase is needed."))
	return rows
}

// reportHints lists the live keys of the current report step.
func (m Model) reportHints() []string {
	if m.report.step == reportPick {
		return []string{
			hint("↑/↓", "move"), hint("→", "open folder"), hint("tab", "path"),
			hint("enter", "choose"), hint("esc", "back"),
		}
	}
	return []string{hint("enter", "another"), hint("esc", "back"), hint("q", "quit")}
}
