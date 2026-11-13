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
	colorErr    = "#FF5555"
	colorOK     = "#00FF87"
	colorInk    = "#1A1200"
)

var (
	styleAccent     = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent))
	styleAccentBold = lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Bold(true)
	styleText       = lipgloss.NewStyle().Foreground(lipgloss.Color(colorText))
	styleDim        = lipgloss.NewStyle().Foreground(lipgloss.Color(colorDim))
	styleWarn       = lipgloss.NewStyle().Foreground(lipgloss.Color(colorWarn))
	styleErr        = lipgloss.NewStyle().Foreground(lipgloss.Color(colorErr))
	styleOK         = lipgloss.NewStyle().Foreground(lipgloss.Color(colorOK))
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

// clip shortens s to at most n cells, marking the cut. Names and paths are the
// only untrusted-length strings on screen, and a wrapped line would break the
// panel's border.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

// bar renders fraction as a fixed-width meter, filled part first.
func bar(fraction float64, width int) string {
	if width <= 0 {
		return ""
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("·", width-filled)
}

// meter colours a bar by how alarming the value is: the fill and passphrase
// meters share it so the same colour always means the same thing.
func meter(fraction float64, width int, warn, bad float64) string {
	s := bar(fraction, width)
	switch {
	case fraction >= bad:
		return styleErr.Render(s)
	case fraction >= warn:
		return styleWarn.Render(s)
	default:
		return styleOK.Render(s)
	}
}

// masked hides a passphrase behind one bullet per byte.
func masked(pass []byte) string {
	return strings.Repeat("•", len(pass))
}

// humanBytes formats a byte count the way the CLI report does.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}
