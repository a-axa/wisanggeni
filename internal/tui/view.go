package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const panelPad = 2

// View implements tea.Model. It rebuilds the whole frame from model state and
// lets Bubble Tea diff it against the previous frame.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return m.viewTooSmall()
	}
	box := m.width
	if box > maxFrame {
		box = maxFrame
	}
	block := lipgloss.JoinVertical(
		lipgloss.Left,
		"",
		m.panelView(box),
		"",
		m.hintBar(box),
	)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, block)
}

// panelView draws the rounded card that holds every screen. The width is the
// full frame: lipgloss counts padding inside Width, so Width(box-2) plus the
// two border columns lands exactly on box.
func (m Model) panelView(box int) string {
	inner := box - 2 - 2*panelPad
	body := lipgloss.JoinVertical(lipgloss.Left, m.screenRows(inner)...)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colorAccent)).
		Padding(0, panelPad).
		Width(box - 2).
		Render(body)
}

func (m Model) screenRows(inner int) []string {
	switch m.screen {
	case screenMenu:
		return m.menuRows(inner)
	case screenHelp:
		return m.helpRows()
	default:
		return m.flowRows()
	}
}

func (m Model) menuRows(inner int) []string {
	rows := make([]string, 0, 20)
	rows = append(rows, "")
	rows = append(rows, fireRows(m.frame)...)
	rows = append(rows, "")
	subtitle := styleDim.Render("hide files and folders inside PNG images")
	if m.version != "" {
		subtitle += styleDim.Render("  ·  ") + styleAccent.Render(m.version)
	}
	rows = append(rows, subtitle, "")
	rows = append(rows, styleDim.Render(strings.Repeat("─", inner)), "")
	for i := range menuItems {
		rows = append(rows, m.menuRow(i, inner))
	}
	return rows
}

func (m Model) menuRow(i, inner int) string {
	const labelCol = 10
	item := menuItems[i]
	label := pad(item.label, labelCol)
	if i == m.menu {
		return styleSelected.Width(inner).Render("▸ " + label + item.desc)
	}
	return styleDim.Render("  ") + styleText.Render(label) + styleDim.Render(item.desc)
}

func (m Model) helpRows() []string {
	rows := []string{"", styleAccent.Render("▸ ") + styleAccentBold.Render("Help"), ""}
	keys := [][2]string{
		{"Up/Down, k/j", "move"},
		{"Enter", "select or continue"},
		{"Space", "add or remove an input in Hide"},
		{"Tab", "move between fields"},
		{"Esc", "back"},
		{"q", "quit"},
		{"?", "toggle this help"},
	}
	for _, kv := range keys {
		rows = append(rows, styleAccent.Render(pad(kv[0], 16))+styleText.Render(kv[1]))
	}
	return append(rows, "", styleDim.Render("wisanggeni "+m.version+" - CLI + TUI"))
}

func (m Model) flowRows() []string {
	return []string{
		"",
		styleAccent.Render("▸ ") + styleAccentBold.Render(m.title()),
		"",
		styleWarn.Render("Not available in this build yet."),
		"",
		styleDim.Render("For now use the CLI: wisanggeni --help"),
	}
}

func (m Model) hintBar(box int) string {
	sep := styleDim.Render("   ")
	var hints []string
	switch m.screen {
	case screenMenu:
		hints = []string{
			styleAccent.Render("↑/↓") + styleDim.Render(" move"),
			styleAccent.Render("enter") + styleDim.Render(" select"),
			styleAccent.Render("?") + styleDim.Render(" help"),
			styleAccent.Render("q") + styleDim.Render(" quit"),
		}
	case screenHelp:
		hints = []string{
			styleAccent.Render("esc") + styleDim.Render(" close"),
			styleAccent.Render("q") + styleDim.Render(" quit"),
		}
	default:
		hints = []string{
			styleAccent.Render("esc") + styleDim.Render(" back"),
			styleAccent.Render("?") + styleDim.Render(" help"),
			styleAccent.Render("q") + styleDim.Render(" quit"),
		}
	}
	return lipgloss.PlaceHorizontal(box, lipgloss.Center, strings.Join(hints, sep))
}

// viewTooSmall is the one screen that ignores the frame: it has to render
// inside whatever tiny terminal it is given.
func (m Model) viewTooSmall() string {
	lines := []string{
		styleAccentBold.Render("Wisanggeni"),
		"",
		styleWarn.Render("The TUI needs at least 80x24."),
		styleDim.Render("Current terminal: ") + styleText.Render(fmt.Sprintf("%dx%d", m.width, m.height)),
		"",
		styleDim.Render("Enlarge the terminal, or use the CLI:"),
		styleText.Render("  wisanggeni hide secret.txt -c cover.png -o out.png"),
		styleText.Render("  wisanggeni score cover.png"),
		"",
		styleDim.Render("q: quit"),
	}
	return strings.Join(lines, "\n")
}
