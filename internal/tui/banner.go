package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// logoRows is the WISANGGENI wordmark drawn with half-block glyphs. The face is
// a 5-row Figlet "mono9"; the widest row is 69 columns, which leaves room for
// the panel inset at the 80-column minimum.
var logoRows = []string{
	"▄     ▄ ▄▄▄▄▄   ▄▄▄▄    ▄▄   ▄▄   ▄   ▄▄▄    ▄▄▄  ▄▄▄▄▄▄ ▄▄   ▄ ▄▄▄▄▄",
	"█  █  █   █    █▀   ▀   ██   █▀▄  █ ▄▀   ▀ ▄▀   ▀ █      █▀▄  █   █",
	"▀ █▀█ █   █    ▀█▄▄▄   █  █  █ █▄ █ █   ▄▄ █   ▄▄ █▄▄▄▄▄ █ █▄ █   █",
	" ██ ██▀   █        ▀█  █▄▄█  █  █ █ █    █ █    █ █      █  █ █   █",
	" █   █  ▄▄█▄▄  ▀▄▄▄█▀ █    █ █   ██  ▀▄▄▄▀  ▀▄▄▄▀ █▄▄▄▄▄ █   ██ ▄▄█▄▄",
}

// heatLevels is how many colour steps the flame resolves to per cell. Sixteen
// steps keep the escape sequence count down without visible banding.
const heatLevels = 16

// fireStops is the flame ramp, from a deep ember to white hot.
var fireStops = []struct {
	at float64
	c  rgb
}{
	{0.00, rgb{80, 6, 0}},
	{0.30, rgb{196, 28, 0}},
	{0.55, rgb{255, 92, 0}},
	{0.78, rgb{255, 176, 0}},
	{1.00, rgb{255, 246, 190}},
}

// Banner returns the wordmark as plain text, one row per line.
func Banner() string { return strings.Join(logoRows, "\n") }

// fireRows paints the wordmark for one animation frame. Heat rises towards the
// bottom of the glyphs, so the logo reads as flames with the hottest core at
// the baseline.
func fireRows(frame int) []string {
	rows := make([]string, len(logoRows))
	for r, line := range logoRows {
		rows[r] = fireLine(line, r, frame)
	}
	return rows
}

func fireLine(line string, row, frame int) string {
	runes := []rune(line)
	var b strings.Builder
	b.Grow(len(line) * 3)
	for i := 0; i < len(runes); {
		if runes[i] == ' ' {
			b.WriteRune(' ')
			i++
			continue
		}
		step := heatStep(cellHeat(row, i, frame))
		j := i + 1
		for j < len(runes) && runes[j] != ' ' && heatStep(cellHeat(row, j, frame)) == step {
			j++
		}
		b.WriteString(lipgloss.NewStyle().
			Foreground(lipgloss.Color(fireColor(float64(step) / (heatLevels - 1)))).
			Render(string(runes[i:j])))
		i = j
	}
	return b.String()
}

// cellHeat is the temperature of one glyph cell: warmer near the baseline and
// jittered per column so neighbours flicker out of phase.
func cellHeat(row, col, frame int) float64 {
	base := 0.30 + 0.70*float64(row)/float64(len(logoRows)-1)
	amp := 0.30 - 0.04*float64(row)
	return base + amp*(flicker(col, frame)-0.5)
}

// flicker is a cheap deterministic hash in [0,1). It only has to look random,
// so an integer mix beats a real PRNG here.
func flicker(col, frame int) float64 {
	h := uint32(col)*0x9E3779B1 ^ uint32(frame)*0x85EBCA6B
	h ^= h >> 15
	h *= 0x2545F491
	h ^= h >> 13
	return float64(h&0xFFFF) / 0x10000
}

func heatStep(t float64) int {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return heatLevels - 1
	}
	return int(t*float64(heatLevels-1) + 0.5)
}

func fireColor(t float64) string {
	for i := 1; i < len(fireStops); i++ {
		if t <= fireStops[i].at {
			a, b := fireStops[i-1], fireStops[i]
			return lerp(a.c, b.c, (t-a.at)/(b.at-a.at)).hex()
		}
	}
	return fireStops[len(fireStops)-1].c.hex()
}
