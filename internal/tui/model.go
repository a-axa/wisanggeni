// Package tui renders the interactive front end. It owns presentation and key
// handling only: every operation it exposes calls the same internal packages the
// CLI uses, so both surfaces stay byte-identical.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/a-axa/wisanggeni/internal/hide"
)

const (
	minWidth  = 80
	minHeight = 24
	maxFrame  = 96
)

// frameInterval is how often the flame repaints. Fast enough to read as fire,
// slow enough to stay cheap on a small terminal.
const frameInterval = 90 * time.Millisecond

type screen int

const (
	screenMenu screen = iota
	screenHide
	screenReveal
	screenScore
	screenCapacity
	screenInspect
	screenHelp
)

type menuItem struct {
	label  string
	desc   string
	screen screen
	quit   bool
}

var menuItems = []menuItem{
	{"Hide", "pack and embed files into a carrier", screenHide, false},
	{"Reveal", "extract a hidden payload", screenReveal, false},
	{"Score", "rate a carrier before hiding", screenScore, false},
	{"Capacity", "show the byte budget for a carrier", screenCapacity, false},
	{"Inspect", "read the container header and entries", screenInspect, false},
	{"Quit", "leave the TUI", screenMenu, true},
}

// tickMsg advances the flame animation by one frame.
type tickMsg struct{}

// Model is the Bubble Tea model for the interactive front end. All state moves
// through Update, so the whole flow can be tested without a terminal.
type Model struct {
	version  string
	width    int
	height   int
	frame    int
	menu     int
	screen   screen
	previous screen
	quitting bool

	hide      hideWiz
	reveal    revealWiz
	report    reportWiz
	inspector inspectorWiz

	// gen tags every asynchronous job. A message whose tag no longer matches is
	// stale — the user went back, or started another job — and is dropped.
	gen int

	// send posts messages from a worker goroutine. It is nil in tests, which is
	// why the stage feed is optional rather than required.
	send func(tea.Msg)
}

// New builds the root model. It starts at the minimum size so the first frame
// is valid even before Bubble Tea delivers a WindowSizeMsg.
func New(version string) Model {
	return Model{
		version:   version,
		width:     minWidth,
		height:    minHeight,
		hide:      newHideWiz(),
		reveal:    newRevealWiz(),
		report:    newReportWiz(modeScore),
		inspector: newInspectorWiz(),
	}
}

// WithSender gives the model a way to report progress from a worker goroutine.
// cmd/tui.go wires it to tea.Program.Send; leaving it unset keeps the model
// usable in tests, where no program exists.
func (m Model) WithSender(send func(tea.Msg)) Model {
	m.send = send
	return m
}

// Init implements tea.Model. It starts the flame animation.
func (m Model) Init() tea.Cmd { return tick() }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tickMsg:
		m.frame++
		return m, tick()
	case planMsg:
		return m.applyPlan(msg), nil
	case stageMsg:
		return m.applyStage(msg), nil
	case runMsg:
		return m.applyRun(msg)
	case revealMsg:
		return m.applyReveal(msg), nil
	case describeMsg:
		return m.applyDescribe(msg), nil
	case payloadMsg:
		return m.applyPayload(msg), nil
	}
	return m, nil
}

func tick() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The flows see the key first. While a text field has focus they claim it,
	// so `q` and `?` are ordinary characters there instead of quitting or
	// opening help mid-passphrase.
	switch m.screen {
	case screenHide:
		if next, cmd, handled := m.handleHideKey(k); handled {
			return next, cmd
		}
	case screenReveal:
		if next, cmd, handled := m.handleRevealKey(k); handled {
			return next, cmd
		}
	case screenScore, screenCapacity:
		if next, cmd, handled := m.handleReportKey(k); handled {
			return next, cmd
		}
	case screenInspect:
		if next, cmd, handled := m.handleInspectKey(k); handled {
			return next, cmd
		}
	}

	if keyQuit(k) {
		m.quitting = true
		return m, tea.Quit
	}
	if keyHelp(k) {
		// The flows claim every key while a field has focus, so a `?` that gets
		// here was not typed into one.
		if m.screen == screenHelp {
			m.screen = m.previous
		} else {
			m.previous = m.screen
			m.screen = screenHelp
		}
		return m, nil
	}
	if keyBack(k) {
		switch m.screen {
		case screenMenu:
			m.quitting = true
			return m, tea.Quit
		case screenHelp:
			m.screen = m.previous
		default:
			m.screen = screenMenu
		}
		return m, nil
	}
	if m.screen != screenMenu {
		return m, nil
	}
	switch {
	case keyUp(k):
		m.menu = (m.menu - 1 + len(menuItems)) % len(menuItems)
	case keyDown(k):
		m.menu = (m.menu + 1) % len(menuItems)
	case keySelect(k):
		return m.activate()
	}
	return m, nil
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	item := menuItems[m.menu]
	if item.quit {
		m.quitting = true
		return m, tea.Quit
	}
	m.screen = item.screen
	switch item.screen {
	case screenHide:
		m.hide.reset()
		m.hide.pick = newPicker(workingDir(), nil)
		m.hide.carrier = newPicker(workingDir(), pngOnly)
	case screenReveal:
		m.reveal.reset()
		m.reveal.pick = newPicker(workingDir(), pngOnly)
	case screenScore:
		m.report.reset(modeScore)
	case screenCapacity:
		m.report.reset(modeCapacity)
	case screenInspect:
		m.inspector.reset()
	}
	return m, nil
}

// leaveFlow returns to the menu and drops whatever the flow was holding. The
// generation bump discards any measurement still in flight, so a result for a
// carrier the user has left cannot land on a later screen.
func (m Model) leaveFlow() Model {
	m.screen = screenMenu
	m.gen++
	m.hide.reset()
	m.reveal.reset()
	m.report.reset(m.report.mode)
	m.inspector.reset()
	return m
}

// applyPlan stores an Analyze result.
func (m Model) applyPlan(msg planMsg) Model {
	if msg.seq != m.gen {
		return m
	}
	m.hide.analyzing = false
	m.hide.plan = msg.plan
	m.hide.planErr = msg.err
	m.hide.planOutput = m.hide.output
	return m
}

// applyStage records progress from the writer.
func (m Model) applyStage(msg stageMsg) Model {
	if msg.seq != m.gen || m.hide.step != stepWorking {
		return m
	}
	m.hide.stage = msg.stage
	if m.hide.stageDone == nil {
		m.hide.stageDone = map[hide.Stage]bool{}
	}
	m.hide.stageDone[msg.stage] = true
	return m
}

// applyRun lands a finished hide. A failure returns to the passphrase step so
// the user can retype it; anything else was already refused by Analyze.
func (m Model) applyRun(msg runMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.gen {
		return m, nil
	}
	if msg.err != nil {
		m.hide.runErr = msg.err
		m.hide.step = stepSecret
		m.hide.field = 1
		return m, nil
	}
	m.hide.runErr = nil
	m.hide.result = msg.result
	m.hide.step = stepDone
	m.hide.stageDone[hide.StageVerify] = true
	// The secret has done its work; drop it rather than keep it on screen state.
	clearBytes(m.hide.pass)
	clearBytes(m.hide.confirm)
	return m, nil
}

// applyReveal lands a finished extraction.
func (m Model) applyReveal(msg revealMsg) Model {
	if msg.seq != m.gen {
		return m
	}
	m.reveal.working = false
	if msg.err != nil {
		m.reveal.err = msg.err
		m.reveal.step = revealSecret
		return m
	}
	m.reveal.err = nil
	m.reveal.result = msg.result
	m.reveal.step = revealDone
	clearBytes(m.reveal.pass)
	return m
}

// clearBytes zeroes a secret in place. Go cannot promise a string is gone, which
// is why the wizard holds passphrases as bytes, like the CLI does.
func clearBytes(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
