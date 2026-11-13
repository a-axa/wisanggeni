package tui

import tea "github.com/charmbracelet/bubbletea"

// The key map is small enough that plain predicates read better than a binding
// table, and it keeps the shell free of the bubbles dependency.

func keyUp(k tea.KeyMsg) bool     { return k.String() == "up" || k.String() == "k" }
func keyDown(k tea.KeyMsg) bool   { return k.String() == "down" || k.String() == "j" }
func keyLeft(k tea.KeyMsg) bool   { return k.String() == "left" || k.String() == "h" }
func keyRight(k tea.KeyMsg) bool  { return k.String() == "right" || k.String() == "l" }
func keySelect(k tea.KeyMsg) bool { return k.String() == "enter" }
func keyBack(k tea.KeyMsg) bool   { return k.String() == "esc" }
func keyHelp(k tea.KeyMsg) bool   { return k.String() == "?" }
func keyTab(k tea.KeyMsg) bool    { return k.String() == "tab" }
func keySpace(k tea.KeyMsg) bool  { return k.String() == " " }
func keyErase(k tea.KeyMsg) bool  { return k.String() == "backspace" }

func keyQuit(k tea.KeyMsg) bool {
	s := k.String()
	return s == "q" || s == "ctrl+c"
}

// typed returns the text a key inserts into a focused field, if any. Bubble Tea
// delivers a whole rune batch per keystroke, so a paste arrives as one message.
func typed(k tea.KeyMsg) (string, bool) {
	switch k.Type {
	case tea.KeyRunes:
		return string(k.Runes), true
	case tea.KeySpace:
		return " ", true
	default:
		return "", false
	}
}

// erase drops the last rune of an editable buffer, walking back over any UTF-8
// continuation bytes so the field never renders half a glyph.
func erase(buf []byte) []byte {
	if len(buf) == 0 {
		return buf
	}
	i := len(buf) - 1
	for i > 0 && buf[i]&0xC0 == 0x80 {
		i--
	}
	return buf[:i]
}
