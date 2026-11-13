package tui

import tea "github.com/charmbracelet/bubbletea"

// The key map is small enough that plain predicates read better than a binding
// table, and it keeps the shell free of the bubbles dependency.

func keyUp(k tea.KeyMsg) bool     { return k.String() == "up" || k.String() == "k" }
func keyDown(k tea.KeyMsg) bool   { return k.String() == "down" || k.String() == "j" }
func keySelect(k tea.KeyMsg) bool { return k.String() == "enter" }
func keyBack(k tea.KeyMsg) bool   { return k.String() == "esc" }
func keyHelp(k tea.KeyMsg) bool   { return k.String() == "?" }

func keyQuit(k tea.KeyMsg) bool {
	s := k.String()
	return s == "q" || s == "ctrl+c"
}
