package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/a-axa/wisanggeni/internal/hide"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// row2 lays a left label and a right-aligned value on one line, clipped to the
// panel. left must be plain text: clip measures bytes, so styling it first would
// count escape codes as content.
func row2(left, right string, inner int) string {
	rw := lipgloss.Width(right)
	if rw >= inner {
		return clip(right, inner)
	}
	left = clip(left, inner-rw-1)
	gap := inner - lipgloss.Width(left) - rw
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// headerRows is the flow's fixed top: the wordmark badge, the step, and a rule.
// The animated banner stays on the menu — repeating five rows of it would eat a
// third of a 24-row terminal.
func (m Model) headerRows(flow string, step, total int, inner int) []string {
	badge := styleSelected.Render(" WISANGGENI ")
	trail := ""
	if step > 0 {
		trail = fmt.Sprintf("  ·  step %d/%d", step, total)
	}
	head := badge + "  " + styleAccentBold.Render(strings.ToLower(flow)) + styleDim.Render(trail)
	return []string{"", head, styleDim.Render(strings.Repeat("─", inner)), ""}
}

// flowHeader is headerRows for the automatic screens, which are not a numbered
// step of anything.
func (m Model) flowHeader(flow, state string, inner int) []string {
	badge := styleSelected.Render(" WISANGGENI ")
	head := badge + "  " + styleAccentBold.Render(flow) + styleDim.Render("  ·  "+state)
	return []string{"", head, styleDim.Render(strings.Repeat("─", inner)), ""}
}

// fieldLine draws an editable field with a caret when it has focus.
func fieldLine(label, value string, focused bool, inner int) string {
	prefix := "  "
	if label != "" {
		prefix = styleDim.Render(pad(label, 10))
	}
	room := inner - lipgloss.Width(prefix)
	if room < 4 {
		room = 4
	}
	if focused {
		return prefix + styleAccent.Render("▸ ") + styleText.Render(clip(value, room-4)) + styleAccent.Render("│")
	}
	return prefix + styleDim.Render("  ") + styleText.Render(clip(value, room-2))
}

// checkbox renders a toggle row.
func checkbox(label string, on bool, inner int) string {
	mark := "[ ]"
	if on {
		mark = "[x]"
	}
	if on {
		return styleAccent.Render(mark) + " " + styleText.Render(clip(label, inner-4))
	}
	return styleDim.Render(mark) + " " + styleDim.Render(clip(label, inner-4))
}

// noteRow renders a soft warning under a list.
func noteRow(text string) string {
	if text == "" {
		return ""
	}
	return styleWarn.Render("! ") + styleWarn.Render(text)
}

// pickerRows draws the directory listing plus its manual path field.
func (m Model) pickerRows(p picker, inner, room int) []string {
	rows := []string{styleDim.Render(clip(shortenDir(p.dir), inner))}
	for _, i := range p.window(room) {
		entry := p.entries[i]
		name := entry.name
		if entry.dir {
			name += "/"
		}
		detail := ""
		if !entry.dir {
			detail = humanBytes(entry.size)
		}
		if i == p.cursor {
			rows = append(rows, styleSelected.Render(row2("▸ "+name, detail, inner)))
			continue
		}
		label := styleText.Render("  " + name)
		if entry.dir {
			label = styleAccent.Render("  " + name)
		}
		rows = append(rows, row2Styled(label, detail, inner))
	}
	if len(p.entries) == 0 {
		rows = append(rows, styleDim.Render("  "+clip(p.note, inner-2)))
	}
	return rows
}

// row2Styled is row2 for a left cell that already carries styling: the styled
// side is measured, never clipped.
func row2Styled(left, right string, inner int) string {
	rw := 0
	if right != "" {
		rw = lipgloss.Width(right) + 1
	}
	if lipgloss.Width(left)+rw > inner {
		return clip(left, inner)
	}
	return left + strings.Repeat(" ", inner-lipgloss.Width(left)-rw) + right
}

// hideRows renders the hide flow.
func (m Model) hideRows(inner, maxRows int) []string {
	w := &m.hide
	var rows []string
	switch w.step {
	case stepInput:
		rows = m.hideInputRows(w, inner, maxRows)
	case stepCarrier:
		rows = m.hideCarrierRows(w, inner, maxRows)
	case stepReport:
		rows = m.hideReportRows(w, inner)
	case stepSecret:
		rows = m.hideSecretRows(w, inner)
	case stepWorking:
		rows = m.hideWorkingRows(w, inner)
	case stepDone:
		rows = m.hideDoneRows(w, inner)
	}
	return fitRows(rows, maxRows)
}

func (m Model) hideInputRows(w *hideWiz, inner, maxRows int) []string {
	rows := m.headerRows("HIDE", 1, hideStepCount, inner)
	rows = append(rows, styleDim.Render(fmt.Sprintf("Chosen (%d)", len(w.inputs))))
	if len(w.inputs) == 0 {
		rows = append(rows, styleDim.Render("  nothing chosen yet - space adds the highlighted row"))
	}
	for i, item := range w.inputs {
		if i == 3 {
			rows = append(rows, styleDim.Render(fmt.Sprintf("  … and %d more", len(w.inputs)-i)))
			break
		}
		rows = append(rows, row2Styled(styleOK.Render("[x] ")+filepath.Base(item.path), item.info, inner))
	}
	rows = append(rows, "")
	room := maxRows - len(rows) - 5
	if room < 3 {
		room = 3
	}
	rows = append(rows, m.pickerRows(w.pick, inner, room)...)
	rows = append(rows, "")
	rows = append(rows, fieldLine("path", w.pick.manual, w.pick.typing, inner))
	if w.pick.note != "" {
		rows = append(rows, noteRow(clip(w.pick.note, inner)))
	}
	return rows
}

func (m Model) hideCarrierRows(w *hideWiz, inner, maxRows int) []string {
	rows := m.headerRows("HIDE", 2, hideStepCount, inner)
	rows = append(rows, row2("Input", chosenSummary(w.inputs), inner))
	carrier := styleDim.Render("none yet")
	if w.carrierPath != "" {
		carrier = styleText.Render(filepath.Base(w.carrierPath))
	}
	rows = append(rows, row2("Carrier", carrier, inner))
	rows = append(rows, "")
	room := maxRows - len(rows) - 5
	if room < 3 {
		room = 3
	}
	rows = append(rows, m.pickerRows(w.carrier, inner, room)...)
	rows = append(rows, "")
	rows = append(rows, fieldLine("path", w.carrier.manual, w.carrier.typing, inner))
	if w.carrier.note != "" {
		rows = append(rows, noteRow(clip(w.carrier.note, inner)))
	}
	return rows
}

// chosenSummary names the chosen inputs on one line.
func chosenSummary(items []inputItem) string {
	if len(items) == 0 {
		return "none"
	}
	names := make([]string, 0, len(items))
	for i, item := range items {
		if i == 2 {
			names = append(names, fmt.Sprintf("+%d more", len(items)-i))
			break
		}
		names = append(names, filepath.Base(item.path))
	}
	return strings.Join(names, " + ")
}

func (m Model) hideReportRows(w *hideWiz, inner int) []string {
	rows := m.headerRows("HIDE", 3, hideStepCount, inner)
	if w.plan == nil || w.analyzing {
		rows = append(rows, spinner(m.frame)+styleDim.Render(" scoring the carrier and packing the payload…"))
		return rows
	}

	p := w.plan
	carrier := styleText.Render(filepath.Base(w.carrierPath)) +
		styleDim.Render(fmt.Sprintf("  %dx%d", p.Width, p.Height))
	rows = append(rows, row2("Carrier", carrier, inner))
	verdict := verdictStyle(p.Score.Verdict).Render(p.Score.Verdict.String())
	rows = append(rows, row2(fmt.Sprintf("textured_fraction %.3f", p.Score.TexturedFraction), verdict, inner))
	rows = append(rows, row2("median_rho", fmt.Sprintf("%.6g", p.Score.MedianRho), inner))
	rows = append(rows, row2("colour chunks", chunkLabel(p.Chunks), inner))
	rows = append(rows, "")

	rows = append(rows, row2(
		fmt.Sprintf("capacity  raw %d", stego.CapacityRaw(p.Width, p.Height)),
		fmt.Sprintf("usable %d", p.Capacity), inner))
	rows = append(rows, row2(
		fmt.Sprintf("          suggested %d (10%%)", p.Suggested),
		fmt.Sprintf("strong %d (5%%)", p.Strong), inner))
	rows = append(rows, "")
	rows = append(rows, row2("blob", fmt.Sprintf("%d bytes (%d payload)", p.BlobSize, p.PayloadSize), inner))
	rows = append(rows, "Fill  "+meter(p.Fill, 24, stego.FillWarn, stego.FillForce)+
		styleDim.Render(fmt.Sprintf("  %.1f%%", p.Fill*100)))
	rows = append(rows, "")
	rows = append(rows, fieldLine("output", w.output, w.field == 1, inner))
	rows = append(rows, checkbox("force: overwrite, fill above 10%, smooth cover  space", w.force, inner))

	for i, msg := range p.Warnings {
		if i == 2 {
			rows = append(rows, styleWarn.Render(fmt.Sprintf("  +%d more warning", len(p.Warnings)-i)))
			break
		}
		rows = append(rows, noteRow(clip(msg, inner-2)))
	}
	if w.planErr != nil {
		rows = append(rows, failureRow(w.planErr, inner))
	}
	return rows
}

// verdictStyle colours the §7.5 verdict the way the CLI reads it.
func verdictStyle(v stego.CoverVerdict) lipgloss.Style {
	switch v {
	case stego.CoverSuitable:
		return styleOK
	case stego.CoverMarginal:
		return styleWarn
	default:
		return styleErr
	}
}

// chunkLabel names the preserved colour chunks.
func chunkLabel(chunks []string) string {
	if len(chunks) == 0 {
		return "none kept"
	}
	return strings.Join(chunks, ", ")
}

func (m Model) hideSecretRows(w *hideWiz, inner int) []string {
	rows := m.headerRows("HIDE", 4, hideStepCount, inner)
	rows = append(rows, row2("Carrier", filepath.Base(w.carrierPath), inner))
	rows = append(rows, row2("Output", w.output, inner))
	if w.plan != nil {
		rows = append(rows, row2("Fill", fmt.Sprintf("%.1f%%", w.plan.Fill*100), inner))
	}
	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Passphrase"))
	rows = append(rows, fieldLine("", masked(w.pass), w.field == 0, inner))
	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Confirm passphrase"))
	rows = append(rows, fieldLine("", masked(w.confirm), w.field == 1, inner))
	rows = append(rows, "")

	label, score := passphraseStrength(w.pass)
	summary := styleDim.Render(label)
	if words := countWords(w.pass); words > 1 {
		summary += styleDim.Render(fmt.Sprintf("  ·  %d words", words))
	}
	rows = append(rows, "Strength  "+meter(score, 24, 0.34, 0.67)+"  "+summary)
	rows = append(rows, "")

	if w.secretNote != "" {
		rows = append(rows, noteRow(w.secretNote))
	}
	if w.runErr != nil {
		rows = append(rows, failureRow(w.runErr, inner))
	}
	return rows
}

func (m Model) hideWorkingRows(w *hideWiz, inner int) []string {
	rows := m.flowHeader("hide", "working", inner)
	rows = append(rows, row2("Input", chosenSummary(w.inputs), inner))
	rows = append(rows, row2("Carrier", filepath.Base(w.carrierPath)+" → "+w.output, inner))
	rows = append(rows, "")
	stages := []hide.Stage{
		hide.StagePack, hide.StageDerive, hide.StageSeal,
		hide.StageEmbed, hide.StageSave, hide.StageVerify,
	}
	for _, stage := range stages {
		rows = append(rows, stageRow(stage, w.stage, w.stageDone[stage]))
	}
	rows = append(rows, "")
	rows = append(rows, emberBand(m.frame, inner, 3)...)
	return rows
}

// stageRow is one line of the checklist: done, running, or pending.
func stageRow(stage, current hide.Stage, done bool) string {
	switch {
	case done:
		return styleOK.Render("✓ ") + styleText.Render(stage.String())
	case stage == current:
		return styleAccentBold.Render("▸ ") + styleAccent.Render(stage.String()) + styleDim.Render("  …")
	default:
		return styleDim.Render("· " + stage.String())
	}
}

func (m Model) hideDoneRows(w *hideWiz, inner int) []string {
	rows := m.flowHeader("hide", "done", inner)
	rows = append(rows, styleOK.Render("✓ done")+
		styleText.Render(" - use the same passphrase to reveal it"))
	rows = append(rows, "")
	rows = append(rows, row2("Output", styleText.Render(w.result.Output)+"  "+
		styleDim.Render(humanBytes(w.result.BlobSize)), inner))
	rows = append(rows, row2("Payload", fmt.Sprintf("%d bytes (fill %.1f%%)", w.result.PayloadSize, w.result.Fill*100), inner))

	verify := styleWarn.Render("not checked")
	if w.result.Verified {
		verify = styleOK.Render("ok - the decoded blob matches the source blob")
	}
	rows = append(rows, row2("Verify", verify, inner))
	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Notes"))
	rows = append(rows, styleDim.Render("- send the PNG as a file, never as a photo"))
	rows = append(rows, styleDim.Render("- do not re-save, resize or edit the output"))
	rows = append(rows, "")
	rows = append(rows, emberBand(m.frame, inner, 2)...)
	return rows
}

// revealRows renders the reveal flow.
func (m Model) revealRows(inner, maxRows int) []string {
	w := &m.reveal
	var rows []string
	switch w.step {
	case revealPick:
		rows = m.revealPickRows(w, inner, maxRows)
	case revealSecret:
		rows = m.revealSecretRows(w, inner)
	case revealWorking:
		rows = m.revealWorkingRows(w, inner)
	case revealDone:
		rows = m.revealDoneRows(w, inner)
	}
	return fitRows(rows, maxRows)
}

func (m Model) revealPickRows(w *revealWiz, inner, maxRows int) []string {
	rows := m.headerRows("REVEAL", 1, 2, inner)
	image := styleDim.Render("none yet")
	if w.image != "" {
		image = styleText.Render(filepath.Base(w.image))
	}
	rows = append(rows, row2("Image", image, inner))
	rows = append(rows, "")
	room := maxRows - len(rows) - 7
	if room < 3 {
		room = 3
	}
	rows = append(rows, m.pickerRows(w.pick, inner, room)...)
	rows = append(rows, "")
	rows = append(rows, fieldLine("path", w.pick.manual, w.pick.typing, inner))
	rows = append(rows, fieldLine("output", w.outDir, w.field == 1, inner))
	rows = append(rows, checkbox("overwrite existing files  space", w.force, inner))
	if w.pick.note != "" {
		rows = append(rows, noteRow(clip(w.pick.note, inner)))
	}
	if w.err != nil {
		rows = append(rows, failureRow(w.err, inner))
	}
	return rows
}

func (m Model) revealSecretRows(w *revealWiz, inner int) []string {
	rows := m.headerRows("REVEAL", 2, 2, inner)
	rows = append(rows, row2("Image", filepath.Base(w.image), inner))
	rows = append(rows, row2("Output dir", w.outDir, inner))
	if w.force {
		rows = append(rows, row2("Overwrite", styleWarn.Render("existing files will be replaced"), inner))
	}
	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Passphrase"))
	rows = append(rows, fieldLine("", masked(w.pass), true, inner))
	rows = append(rows, "")
	if len(w.pass) == 0 {
		rows = append(rows, styleDim.Render("the passphrase is asked once: reveal never confirms it"))
	}
	if w.err != nil {
		rows = append(rows, failureRow(w.err, inner))
	}
	return rows
}

func (m Model) revealWorkingRows(_ *revealWiz, inner int) []string {
	rows := m.flowHeader("reveal", "working", inner)
	rows = append(rows,
		spinner(m.frame)+styleDim.Render("  deriving the key, decrypting and unpacking…"),
		styleDim.Render("  Argon2id is deliberately slow; this is the cost of the passphrase"),
		"")
	rows = append(rows, emberBand(m.frame, inner, 4)...)
	return rows
}

func (m Model) revealDoneRows(w *revealWiz, inner int) []string {
	rows := m.flowHeader("reveal", "done", inner)
	rows = append(rows, styleOK.Render("✓ done")+
		styleText.Render(fmt.Sprintf(" - %d entries, %s", len(w.result.Entries), humanBytes(w.result.Total))))
	rows = append(rows, "")
	rows = append(rows, row2("Output dir", w.result.Dir, inner))
	rows = append(rows, "")
	rows = append(rows, styleDim.Render("Entries"))
	for i, entry := range w.result.Entries {
		if i == 8 {
			rows = append(rows, styleDim.Render(fmt.Sprintf("  … and %d more", len(w.result.Entries)-i)))
			break
		}
		name := entry.Name
		detail := humanBytes(entry.Size)
		if entry.Dir {
			name += "/"
			detail = "dir"
		}
		rows = append(rows, row2Styled(styleText.Render("  "+name), styleDim.Render(detail), inner))
	}
	return rows
}

// fitRows keeps the panel inside the terminal: too many rows would push the
// border and the hint bar off screen.
func fitRows(rows []string, limit int) []string {
	if limit <= 0 || len(rows) <= limit {
		return rows
	}
	cut := rows[:limit]
	cut[limit-1] = styleDim.Render("… the rest does not fit; enlarge the terminal")
	return cut
}
