package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/a-axa/wisanggeni/internal/hide"
)

// editAction is what a keystroke means to a text field. Keys that are not text
// actions fall through to the list the field belongs to.
type editAction int

const (
	editNone    editAction = iota // not a text key
	editChanged                   // buffer changed
	editSubmit                    // Enter
	editTab                       // Tab
	editEsc                       // Esc
)

// editText applies a keystroke to a printable buffer.
func editText(s string, k tea.KeyMsg) (string, editAction) {
	switch {
	case keyErase(k):
		return string(erase([]byte(s))), editChanged
	case keyTab(k):
		return s, editTab
	case keySelect(k):
		return s, editSubmit
	case keyBack(k):
		return s, editEsc
	}
	if text, ok := typed(k); ok {
		return s + text, editChanged
	}
	return s, editNone
}

// editSecret applies a keystroke to a passphrase buffer.
func editSecret(buf []byte, k tea.KeyMsg) ([]byte, editAction) {
	switch {
	case keyErase(k):
		return erase(buf), editChanged
	case keyTab(k):
		return buf, editTab
	case keySelect(k):
		return buf, editSubmit
	case keyBack(k):
		return buf, editEsc
	}
	if text, ok := typed(k); ok {
		return append(buf, text...), editChanged
	}
	return buf, editNone
}

// acceptInput is the check for a typed input path: it only has to exist.
func acceptInput(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no such file or folder: %s", filepath.Base(path))
	}
	return nil
}

// acceptCarrier is the check for a typed carrier path: an existing PNG file.
func acceptCarrier(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no such file: %s", filepath.Base(path))
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a folder, not a PNG", filepath.Base(path))
	}
	if !strings.EqualFold(filepath.Ext(path), ".png") {
		return fmt.Errorf("%s is not a PNG", filepath.Base(path))
	}
	return nil
}

// handleHideKey dispatches a key to the current step.
func (m Model) handleHideKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch m.hide.step {
	case stepInput:
		return m.hideInputKey(k)
	case stepCarrier:
		return m.hideCarrierKey(k)
	case stepReport:
		return m.hideReportKey(k)
	case stepSecret:
		return m.hideSecretKey(k)
	case stepWorking:
		// The embed cannot be interrupted: Execute writes atomically and the
		// only thing a cancelled run would leave behind is a temporary file.
		// Ctrl+C still exits the program; Esc is swallowed on purpose.
		if keyQuit(k) {
			return m, tea.Quit, true
		}
		return m, nil, true
	case stepDone:
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

func (m Model) hideInputKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.hide
	if w.pick.typing {
		value, action := editText(w.pick.manual, k)
		w.pick.manual = value
		switch action {
		case editChanged:
			w.pick.note = ""
			return m, nil, true
		case editSubmit:
			if path, ok := w.pick.appendManual(acceptInput); ok {
				w.addInput(path)
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
	case keySpace(k):
		if entry, ok := w.pick.current(); ok {
			w.addInput(filepath.Join(w.pick.dir, entry.name))
			w.pick.note = ""
		}
	case keyTab(k):
		w.pick.typing = true
	case keySelect(k):
		if len(w.inputs) == 0 {
			w.pick.note = "choose at least one file or folder first"
			break
		}
		w.step = stepCarrier
		w.carrier.dir = w.pick.dir
		w.carrier.cursor = 0
		w.carrier.reload()
	case keyBack(k):
		return m.leaveFlow(), nil, true
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) hideCarrierKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.hide
	if w.carrier.typing {
		value, action := editText(w.carrier.manual, k)
		w.carrier.manual = value
		switch action {
		case editChanged:
			w.carrier.note = ""
			return m, nil, true
		case editSubmit:
			if path, ok := w.carrier.appendManual(acceptCarrier); ok {
				w.carrierPath = path
				w.step = stepReport
				next, cmd := m.analyze()
				return next, cmd, true
			}
			return m, nil, true
		case editTab, editEsc:
			w.carrier.typing = false
			return m, nil, true
		default:
			return m, nil, true
		}
	}

	switch {
	case keyUp(k):
		w.carrier.move(-1)
	case keyDown(k):
		w.carrier.move(1)
	case keyRight(k):
		w.carrier.descend()
	case keyLeft(k):
		w.carrier.ascend()
	case keyTab(k):
		w.carrier.typing = true
	case keySelect(k):
		entry, ok := w.carrier.current()
		if !ok {
			break
		}
		if entry.dir {
			w.carrier.descend()
			break
		}
		w.carrierPath = filepath.Join(w.carrier.dir, entry.name)
		w.carrier.note = ""
		w.field = 0
		w.step = stepReport
		next, cmd := m.analyze()
		return next, cmd, true
	case keyBack(k):
		w.step = stepInput
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) hideReportKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.hide
	if w.field == 1 {
		value, action := editText(w.output, k)
		w.output = value
		switch action {
		case editTab, editEsc:
			w.field = 0
			// Leaving a changed output re-scores nothing, but Analyze does check the
			// path for collisions, so the report has to catch up before Enter means
			// "continue".
			if w.planStale() {
				next, cmd := m.analyze()
				return next, cmd, true
			}
			return m, nil, true
		case editSubmit:
			w.field = 0
			return m.reportEnter()
		case editNone:
			return m, nil, false
		}
		return m, nil, true
	}

	switch {
	case keySelect(k):
		return m.reportEnter()
	case keySpace(k):
		// One checkbox for all three overrides, exactly like --force.
		w.force = !w.force
		next, cmd := m.analyze()
		return next, cmd, true
	case keyTab(k):
		w.field = 1
	case keyBack(k):
		w.step = stepCarrier
	default:
		return m, nil, false
	}
	return m, nil, true
}

// reportEnter re-runs Analyze when the report is stale or failed, and otherwise
// moves on to the passphrase. One key can mean both because the screen always
// shows which state it is in.
func (m Model) reportEnter() (Model, tea.Cmd, bool) {
	if m.hide.planStale() || m.hide.planErr != nil || m.hide.analyzing {
		next, cmd := m.analyze()
		return next, cmd, true
	}
	m.hide.step = stepSecret
	m.hide.field = 0
	m.hide.secretNote = ""
	return m, nil, true
}

// analyze starts §7.8 steps 1-6 and tags the job, so a result from a carrier the
// user has since changed is dropped instead of displayed.
func (m Model) analyze() (Model, tea.Cmd) {
	m.gen++
	m.hide.analyzing = true
	m.hide.runErr = nil
	return m, analyzeCmd(m.gen, m.hide.hideOptions())
}

func (m Model) hideSecretKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.hide
	target := &w.pass
	if w.field == 1 {
		target = &w.confirm
	}

	buf, action := editSecret(*target, k)
	*target = buf
	switch action {
	case editChanged:
		w.secretNote = ""
		w.runErr = nil
		return m, nil, true
	case editTab:
		w.field = 1 - w.field
		return m, nil, true
	case editEsc:
		w.step = stepReport
		w.field = 0
		clearBytes(w.pass)
		clearBytes(w.confirm)
		return m, nil, true
	case editNone:
		return m, nil, false
	}

	// Enter.
	note, ok := passphraseNote(w.pass, w.confirm)
	if !ok {
		w.secretNote = note
		return m, nil, true
	}

	m.gen++
	w.secretNote = note
	w.runErr = nil
	w.step = stepWorking
	w.stage = hide.StagePack
	w.stageDone = map[hide.Stage]bool{}
	opts := w.hideOptions()
	plan := w.plan
	return m, runCmd(m.gen, plan, opts, m.send), true
}

func (m Model) handleRevealKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch m.reveal.step {
	case revealPick:
		return m.revealPickKey(k)
	case revealSecret:
		return m.revealSecretKey(k)
	case revealWorking:
		if keyQuit(k) {
			return m, tea.Quit, true
		}
		return m, nil, true
	case revealDone:
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

func (m Model) revealPickKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.reveal

	if w.pick.typing {
		value, action := editText(w.pick.manual, k)
		w.pick.manual = value
		switch action {
		case editChanged:
			w.pick.note = ""
			return m, nil, true
		case editSubmit:
			if path, ok := w.pick.appendManual(acceptCarrier); ok {
				w.image = path
				w.err = nil
				w.pick.typing = false
				w.step = revealSecret
			}
			return m, nil, true
		case editTab:
			w.pick.typing = false
			w.field = 1
			return m, nil, true
		case editEsc:
			w.pick.typing = false
			return m, nil, true
		default:
			return m, nil, true
		}
	}

	if w.field == 1 {
		value, action := editText(w.outDir, k)
		w.outDir = value
		switch action {
		case editTab, editEsc:
			w.field = 0
			return m, nil, true
		case editNone:
			return m, nil, false
		}
		return m, nil, true
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
	case keySpace(k):
		w.force = !w.force
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
		w.image = filepath.Join(w.pick.dir, entry.name)
		w.err = nil
		w.step = revealSecret
	case keyBack(k):
		return m.leaveFlow(), nil, true
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) revealSecretKey(k tea.KeyMsg) (Model, tea.Cmd, bool) {
	w := &m.reveal
	buf, action := editSecret(w.pass, k)
	w.pass = buf
	switch action {
	case editChanged:
		w.err = nil
		return m, nil, true
	case editTab:
		return m, nil, true
	case editEsc:
		w.step = revealPick
		clearBytes(w.pass)
		return m, nil, true
	case editNone:
		return m, nil, false
	}

	if len(w.pass) == 0 {
		w.err = errEmptyPassphrase
		return m, nil, true
	}
	m.gen++
	w.err = nil
	w.step = revealWorking
	return m, revealCmd(m.gen, w.image, w.outDir, w.force, w.pass), true
}
