package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// keyMsg builds the KeyMsg a real terminal would deliver for a named key. The
// model only ever reads the string form, but going through the proper KeyType
// keeps the tests honest about what they exercise.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

func press(t *testing.T, m Model, name string) Model {
	t.Helper()
	next, _ := m.Update(keyMsg(name))
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got
}

func resize(t *testing.T, m Model, width, height int) Model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got
}

func TestMenuItemsInOrder(t *testing.T) {
	want := []string{"Hide", "Reveal", "Score", "Capacity", "Inspect", "Quit"}
	if len(menuItems) != len(want) {
		t.Fatalf("menu has %d items, want %d", len(menuItems), len(want))
	}
	for i, label := range want {
		if menuItems[i].label != label {
			t.Errorf("menu item %d = %q, want %q", i, menuItems[i].label, label)
		}
	}
	if !menuItems[len(menuItems)-1].quit {
		t.Error("last menu item should quit")
	}
}

func TestStartsOnMenu(t *testing.T) {
	m := New("test")
	if m.screen != screenMenu {
		t.Fatalf("screen = %d, want screenMenu", m.screen)
	}
	if m.menu != 0 {
		t.Fatalf("cursor = %d, want 0", m.menu)
	}
}

func TestMenuNavigation(t *testing.T) {
	m := New("test")
	if m = press(t, m, "down"); m.menu != 1 {
		t.Fatalf("after down cursor = %d, want 1", m.menu)
	}
	if m = press(t, m, "j"); m.menu != 2 {
		t.Fatalf("after j cursor = %d, want 2", m.menu)
	}
	if m = press(t, m, "k"); m.menu != 1 {
		t.Fatalf("after k cursor = %d, want 1", m.menu)
	}
	if m = press(t, m, "up"); m.menu != 0 {
		t.Fatalf("after up cursor = %d, want 0", m.menu)
	}
	if m = press(t, m, "up"); m.menu != len(menuItems)-1 {
		t.Fatalf("up from top should wrap to %d, got %d", len(menuItems)-1, m.menu)
	}
	if m = press(t, m, "down"); m.menu != 0 {
		t.Fatalf("down from bottom should wrap to 0, got %d", m.menu)
	}
}

func TestSelectOpensScreenAndBackReturns(t *testing.T) {
	m := New("test")
	m = press(t, m, "down")
	m = press(t, m, "enter")
	if m.screen != screenReveal {
		t.Fatalf("screen = %d, want screenReveal", m.screen)
	}
	m = press(t, m, "esc")
	if m.screen != screenMenu {
		t.Fatalf("screen = %d, want screenMenu after esc", m.screen)
	}
}

func TestSelectQuit(t *testing.T) {
	m := New("test")
	for range len(menuItems) - 1 {
		m = press(t, m, "down")
	}
	next, cmd := m.Update(keyMsg("enter"))
	got := next.(Model)
	if !got.quitting {
		t.Fatal("selecting Quit should set quitting")
	}
	assertQuitCmd(t, cmd)
}

func TestQuitKeys(t *testing.T) {
	for _, name := range []string{"q", "esc", "ctrl+c"} {
		t.Run(name, func(t *testing.T) {
			next, cmd := New("test").Update(keyMsg(name))
			got := next.(Model)
			if !got.quitting {
				t.Fatalf("%q should quit", name)
			}
			assertQuitCmd(t, cmd)
		})
	}
}

func assertQuitCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("quit should return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", cmd())
	}
}

func TestHelpTogglesAndRestores(t *testing.T) {
	m := New("test")
	if m = press(t, m, "?"); m.screen != screenHelp {
		t.Fatalf("screen = %d, want screenHelp", m.screen)
	}
	if m = press(t, m, "?"); m.screen != screenMenu {
		t.Fatalf("screen = %d, want screenMenu", m.screen)
	}
	m = press(t, m, "?")
	if m = press(t, m, "esc"); m.screen != screenMenu {
		t.Fatalf("screen = %d, want screenMenu after esc", m.screen)
	}
}

func TestHelpReturnsToFlowScreen(t *testing.T) {
	m := New("test")
	m = press(t, m, "enter")
	m = press(t, m, "?")
	if m.screen != screenHelp {
		t.Fatalf("screen = %d, want screenHelp", m.screen)
	}
	if m = press(t, m, "esc"); m.screen != screenHide {
		t.Fatalf("screen = %d, want screenHide", m.screen)
	}
}

func TestTickAdvancesFlame(t *testing.T) {
	m := New("test")
	if m.frame != 0 {
		t.Fatalf("frame = %d, want 0", m.frame)
	}
	next, cmd := m.Update(tickMsg{})
	got := next.(Model)
	if got.frame != 1 {
		t.Fatalf("frame = %d, want 1", got.frame)
	}
	if cmd == nil {
		t.Fatal("a tick should schedule the next one")
	}
}

func TestFlameIsHotterAtBaseline(t *testing.T) {
	top := cellHeat(0, 5, 0)
	bottom := cellHeat(len(logoRows)-1, 5, 0)
	if bottom <= top {
		t.Fatalf("heat top=%v bottom=%v, want bottom hotter", top, bottom)
	}
}

func TestBannerIsFiveRows(t *testing.T) {
	rows := strings.Split(Banner(), "\n")
	if len(rows) != 5 {
		t.Fatalf("banner has %d rows, want 5", len(rows))
	}
	for i, row := range rows {
		if w := lipgloss.Width(row); w == 0 || w > 69 {
			t.Errorf("banner row %d width = %d, want 1..69", i, w)
		}
	}
}

func TestTooSmallView(t *testing.T) {
	m := resize(t, New("test"), 60, 20)
	view := m.View()
	for _, want := range []string{"80x24", "60x20"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	for _, unwanted := range []string{"Reveal", "Inspect", "Hide", "Quit"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("view should not show the menu item %q", unwanted)
		}
	}
}

func TestMenuViewFitsFrame(t *testing.T) {
	m := resize(t, New("test"), 80, 24)
	assertFits(t, m.View(), 80, 24)
	for _, want := range []string{"Hide", "Reveal", "Score", "Capacity", "Inspect", "Quit", "▄"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("menu view missing %q", want)
		}
	}
}

func TestWideMenuViewIsCentered(t *testing.T) {
	m := resize(t, New("test"), 120, 40)
	view := m.View()
	assertFits(t, view, 120, 40)
	for _, want := range []string{"Hide", "Reveal", "Inspect", "Quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestHelpViewListsKeys(t *testing.T) {
	m := resize(t, New("test"), 80, 24)
	m = press(t, m, "?")
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"Up/Down", "Enter", "Space", "Tab", "toggle this help"} {
		if !strings.Contains(view, want) {
			t.Errorf("help view missing %q", want)
		}
	}
}

func TestSelectedMenuRowFillsPanel(t *testing.T) {
	m := New("test")
	const inner = 74
	if w := lipgloss.Width(m.menuRow(0, inner)); w != inner {
		t.Fatalf("selected row width = %d, want %d", w, inner)
	}
	if w := lipgloss.Width(m.menuRow(1, inner)); w > inner {
		t.Fatalf("unselected row width = %d, want at most %d", w, inner)
	}
}

func TestFlowViewNamesTheScreen(t *testing.T) {
	m := resize(t, New("test"), 80, 24)
	m = press(t, m, "down")
	m = press(t, m, "down")
	m = press(t, m, "enter")
	view := m.View()
	assertFits(t, view, 80, 24)
	if !strings.Contains(view, "Score") {
		t.Errorf("view missing the screen title, got:\n%s", view)
	}
}

func TestQuittingViewIsEmpty(t *testing.T) {
	m := New("test")
	m = press(t, m, "q")
	if view := m.View(); view != "" {
		t.Fatalf("quitting view = %q, want empty", view)
	}
}

func assertFits(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("view has %d lines, want at most %d", len(lines), height)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("line %d is %d wide, want at most %d", i+1, w, width)
		}
	}
}
