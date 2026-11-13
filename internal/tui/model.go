// Package tui renders the interactive front end. It owns presentation and key
// handling only: every operation it exposes is meant to call the same internal
// packages the CLI uses, so both surfaces stay byte-identical.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
}

// New builds the root model. It starts at the minimum size so the first frame
// is valid even before Bubble Tea delivers a WindowSizeMsg.
func New(version string) Model {
	return Model{version: version, width: minWidth, height: minHeight}
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
	}
	return m, nil
}

func tick() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if keyQuit(k) {
		m.quitting = true
		return m, tea.Quit
	}
	if keyHelp(k) {
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
	return m, nil
}

func (m Model) title() string {
	switch m.screen {
	case screenHide:
		return "Hide"
	case screenReveal:
		return "Reveal"
	case screenScore:
		return "Score"
	case screenCapacity:
		return "Capacity"
	case screenInspect:
		return "Inspect"
	case screenHelp:
		return "Help"
	}
	return "Wisanggeni"
}
