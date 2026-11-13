package tui

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/inspect"
)

// inspectStep is one screen of the inspect flow (§7.10): pick a file, read the
// carrier layer, and optionally unlock the payload layer with a passphrase.
type inspectStep int

const (
	inspectPick inspectStep = iota
	inspectReport
	inspectWorking
	inspectPayload
)

// inspectorWiz is the state of the Inspect screen. Without a passphrase it is
// the carrier layer and nothing else; the payload layer appears only after a
// successful decrypt, and it never touches the disk.
type inspectorWiz struct {
	step inspectStep
	pick picker

	path    string
	info    *inspect.Info
	loading bool

	pass []byte
	note string

	payload *inspect.Payload
	err     error
}

func newInspectorWiz() inspectorWiz {
	return inspectorWiz{pick: newPicker(workingDir(), pngOnly)}
}

func (w *inspectorWiz) reset() { *w = newInspectorWiz() }

// payloadMsg carries a decrypted payload back to the UI goroutine.
type payloadMsg struct {
	seq     int
	payload *inspect.Payload
	err     error
}

// payloadCmd decrypts the payload off the UI goroutine and lists its archive in
// memory. It opens the carrier again rather than holding a decoded image in the
// model: Argon2id dominates the cost, so the second decode is free, and nothing
// large stays resident between screens.
func payloadCmd(seq int, path string, pass []byte) tea.Cmd {
	return func() tea.Msg {
		car, err := carrier.Open(path)
		if err != nil {
			return payloadMsg{seq: seq, err: err}
		}
		payload, err := inspect.OpenPayload(car.Image, pass, false)
		return payloadMsg{seq: seq, payload: payload, err: err}
	}
}

// applyPayload stores a finished decrypt. A failure returns to the passphrase
// field with the same opaque message the CLI prints, so inspect is not an oracle.
func (m Model) applyPayload(msg payloadMsg) Model {
	if msg.seq != m.gen || m.screen != screenInspect {
		return m
	}
	w := &m.inspector
	if msg.err != nil {
		w.err = msg.err
		w.step = inspectReport
		return m
	}
	clearBytes(w.pass)
	w.payload = msg.payload
	w.err = nil
	w.step = inspectPayload
	return m
}

// loadInspect starts reading the carrier for path.
func (m Model) loadInspect(path string) (Model, tea.Cmd) {
	m.gen++
	w := &m.inspector
	w.step = inspectReport
	w.path = path
	w.info = nil
	w.err = nil
	w.note = ""
	w.payload = nil
	clearBytes(w.pass)
	w.loading = true
	return m, describeCmd(m.gen, path)
}

// handleInspectKey dispatches a key on the Inspect screen.
func (m Model) handleInspectKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch m.inspector.step {
	case inspectPick:
		return m.inspectPickKey(k)
	case inspectReport:
		return m.inspectReportKey(k)
	case inspectWorking:
		if keyQuit(k) {
			return m, tea.Quit, true
		}
		return m, nil, true
	case inspectPayload:
		switch {
		case keySelect(k), keyBack(k):
			return m.leaveFlow(), nil, true
		case keyQuit(k):
			return m, tea.Quit, true
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) inspectPickKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.inspector
	if w.pick.typing {
		value, action := editText(w.pick.manual, k)
		w.pick.manual = value
		switch action {
		case editChanged:
			w.pick.note = ""
			return m, nil, true
		case editSubmit:
			if path, ok := w.pick.appendManual(acceptCarrier); ok {
				w.pick.typing = false
				next, cmd := m.loadInspect(path)
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
		next, cmd := m.loadInspect(filepath.Join(w.pick.dir, entry.name))
		return next, cmd, true
	case keyBack(k):
		return m.leaveFlow(), nil, true
	default:
		return m, nil, false
	}
	return m, nil, true
}

// inspectReportKey drives the passphrase field. An empty answer is not a
// failure, unlike in hide and reveal: it just leaves the carrier layer on screen.
func (m Model) inspectReportKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.inspector
	buf, action := editSecret(w.pass, k)
	w.pass = buf
	switch action {
	case editChanged:
		w.err = nil
		w.note = ""
		return m, nil, true
	case editTab:
		return m, nil, true
	case editEsc:
		clearBytes(w.pass)
		w.step = inspectPick
		w.err = nil
		return m, nil, true
	case editNone:
		return m, nil, false
	}

	// Enter.
	if len(w.pass) == 0 {
		w.note = "type a passphrase to decrypt; without one only the carrier is shown"
		return m, nil, true
	}
	if w.loading {
		w.note = "the carrier is still being read"
		return m, nil, true
	}
	m.gen++
	w.err = nil
	w.note = ""
	w.step = inspectWorking
	return m, payloadCmd(m.gen, w.path, w.pass), true
}

// inspectorRows renders the Inspect screen.
func (m Model) inspectorRows(inner, maxRows int) []string {
	switch m.inspector.step {
	case inspectPick:
		return fitRows(m.inspectPickRows(inner, maxRows), maxRows)
	case inspectReport:
		return fitRows(m.inspectReportRows(inner), maxRows)
	case inspectWorking:
		return fitRows(m.inspectWorkingRows(inner), maxRows)
	default:
		return fitRows(m.inspectPayloadRows(inner), maxRows)
	}
}

func (m Model) inspectPickRows(inner, maxRows int) []string {
	w := &m.inspector
	rows := m.flowHeader("Inspect", "choose a PNG", inner)

	room := maxRows - len(rows) - 5
	if room < 3 {
		room = 3
	}
	rows = append(rows, m.pickerRows(w.pick, inner, room)...)
	rows = append(rows, "")
	rows = append(rows, fieldLine("path", w.pick.manual, w.pick.typing, inner))
	rows = append(rows, styleDim.Render("the carrier layer needs no passphrase"))
	if w.err != nil {
		rows = append(rows, failureRow(w.err, inner))
	}
	if w.pick.note != "" {
		rows = append(rows, noteRow(clip(w.pick.note, inner)))
	}
	return rows
}

func (m Model) inspectReportRows(inner int) []string {
	w := &m.inspector
	rows := m.flowHeader("Inspect", clip(filepath.Base(w.path), 40), inner)

	if w.loading {
		rows = append(rows, spinner(m.frame)+styleDim.Render(" reading the carrier…"))
		return rows
	}
	if w.info == nil {
		if w.err != nil {
			rows = append(rows, failureRow(w.err, inner), "")
			rows = append(rows, styleDim.Render("enter or esc returns to the file list"))
		}
		return rows
	}

	info := w.info
	rows = append(rows, row2("dimensions", fmt.Sprintf("%dx%d", info.Width, info.Height), inner))
	rows = append(rows, row2("channels", fmt.Sprintf("%d", info.Channels), inner))
	if info.Alpha {
		rows = append(rows, row2("alpha", styleWarn.Render("yes - cannot be used"), inner))
	} else {
		rows = append(rows, row2("alpha", "no", inner))
	}
	rows = append(rows, row2("capacity raw", fmt.Sprintf("%d bytes", info.Raw), inner))
	rows = append(rows, row2("usable", fmt.Sprintf("%d bytes", info.Usable), inner))
	rows = append(rows, row2("colour chunks", chunkLabel(info.Chunks), inner))
	for i, msg := range info.Warnings {
		if i == 3 {
			rows = append(rows, styleWarn.Render(fmt.Sprintf("  +%d more warning", len(info.Warnings)-i)))
			break
		}
		rows = append(rows, noteRow(clip(msg, inner-2)))
	}

	rows = append(rows, "")
	rows = append(rows, styleDim.Render("No passphrase: the carrier layer only."))
	rows = append(rows, styleDim.Render("File names, sizes and contents need the correct passphrase."))
	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Passphrase"))
	rows = append(rows, fieldLine("", masked(w.pass), true, inner))
	if w.note != "" {
		rows = append(rows, noteRow(w.note))
	}
	if w.err != nil {
		rows = append(rows, failureRow(w.err, inner))
	}
	return rows
}

func (m Model) inspectWorkingRows(inner int) []string {
	rows := m.flowHeader("Inspect", "decrypting", inner)
	rows = append(rows,
		spinner(m.frame)+styleDim.Render("  deriving the key, decrypting and listing the archive…"),
		styleDim.Render("  Argon2id is deliberately slow; this is the cost of the passphrase"),
		"")
	rows = append(rows, emberBand(m.frame, inner, 3)...)
	return rows
}

func (m Model) inspectPayloadRows(inner int) []string {
	w := &m.inspector
	p := w.payload
	rows := m.flowHeader("Inspect", clip(filepath.Base(w.path)+"  ·  decrypted", 40), inner)

	rows = append(rows, row2("version", fmt.Sprintf("%d", p.Version), inner))
	flags := "none"
	if p.Compressed {
		flags = "zstd"
	}
	rows = append(rows, row2("flags", flags, inner))
	rows = append(rows, row2("body", fmt.Sprintf("%d bytes", p.BodyLen), inner))

	if !p.HeaderOnly {
		rows = append(rows, row2("entries", fmt.Sprintf("%d (%d files)", len(p.Entries), p.Files), inner))
		rows = append(rows, "")
		rows = append(rows, styleDim.Render("Entries"))
		for i, e := range p.Entries {
			if i == 12 {
				rows = append(rows, styleDim.Render(fmt.Sprintf("  … and %d more", len(p.Entries)-i)))
				break
			}
			name := e.Name
			detail := humanBytes(e.Size)
			if e.Dir {
				name += "/"
				detail = "dir"
			}
			rows = append(rows, row2Styled(styleText.Render("  "+name), styleDim.Render(detail), inner))
		}
		rows = append(rows, "")
		rows = append(rows, row2("total", fmt.Sprintf("%d bytes uncompressed", p.Total), inner))
		if p.Compressed && p.BodyLen > 0 {
			rows = append(rows, row2("ratio", fmt.Sprintf("%.2fx (content : body)",
				float64(p.Total)/float64(p.BodyLen)), inner))
		}
	}

	rows = append(rows, "")
	rows = append(rows, styleDim.Render("This mode writes nothing to disk."))
	return rows
}

// inspectHints lists the live keys of the current inspect step.
func (m Model) inspectHints() []string {
	switch m.inspector.step {
	case inspectPick:
		return []string{
			hint("↑/↓", "move"), hint("→", "open folder"), hint("tab", "path"),
			hint("enter", "choose"), hint("esc", "back"),
		}
	case inspectReport:
		return []string{hint("enter", "decrypt"), hint("esc", "back"), hint("q", "quit")}
	case inspectWorking:
		return []string{hint("…", "decrypting"), hint("q", "quit")}
	default:
		return []string{hint("enter", "menu"), hint("q", "quit")}
	}
}
