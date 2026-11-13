package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette. Amber carries the brand; the rest are the semantic colours the CLI
// already uses for its reports.
const (
	colorAccent = "#FFA500"
	colorText   = "#FFFFFF"
	colorDim    = "#808080"
	colorWarn   = "#FFFF00"
	colorInk    = "#1A1200"
)

var (
	styleAccent     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent))
	styleAccentBold = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Bold(true)
	styleText       = lipgloss.NewStyle().Foreground(lipgloss.Color(colorText))
	styleDim        = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))
	styleWarn       = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarn))
	styleSelected   = lipgloss.NewStyle().
			Background(lipgloss.Color(colorAccent)).
			Foreground(lipgloss.Color(colorInk)).
			Bold(true)
)

// rgb is one 8-bit colour, used to build the flame ramp.
type rgb struct{ r, g, b uint8 }

func (c rgb) hex() string { return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b) }

func lerp(a, b rgb, t float64) rgb {
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5)
	}
	return rgb{mix(a.r, b.r), mix(a.g, b.g), mix(a.b, b.b)}
}

// pad right-pads s with spaces to width n. Inputs are ASCII, so byte length is
// the cell width.
func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
