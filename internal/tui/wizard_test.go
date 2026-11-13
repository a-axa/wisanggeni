package tui

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/hide"
	"github.com/a-axa/wisanggeni/internal/reveal"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// testPassphrase is long enough to skip the short-passphrase warning.
const testPassphrase = "correct horse battery staple"

// pressCmd feeds one keystroke and hands back the command the Bubble Tea runtime
// would now be running.
func pressCmd(t *testing.T, m Model, name string) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(keyMsg(name))
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got, cmd
}

// deliver runs a command and applies its message, the way the runtime does. It
// is only for our own jobs: delivering a tick would schedule another tick and
// never return.
func deliver(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	next, _ := m.Update(cmd())
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got
}

// pressN repeats a keystroke, for clearing a field that has no select-all.
func pressN(t *testing.T, m Model, name string, n int) Model {
	t.Helper()
	for range n {
		m = press(t, m, name)
	}
	return m
}

// noiseCarrierImage builds a carrier CoverScore accepts.
func noiseCarrierImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	state := uint32(0x9e3779b9)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				img.Pix[i+c] = byte(state)
			}
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

// flatCarrierImage builds a carrier CoverScore rejects outright.
func flatCarrierImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 128, 128, 128, 0xff
	}
	return img
}

// writeCarrier saves a PNG carrier and returns its path.
func writeCarrier(t *testing.T, path string, img *image.NRGBA) string {
	t.Helper()
	if err := carrier.Save(path, img, nil); err != nil {
		t.Fatalf("save carrier: %v", err)
	}
	return path
}

// writeFile writes a file and returns its path.
func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}

// randomBytes is a deterministic stand-in for incompressible data, so the fill
// tests do not depend on crypto/rand.
func randomBytes(n int) []byte {
	out := make([]byte, n)
	state := uint32(0x1234567)
	for i := range out {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		out[i] = byte(state)
	}
	return out
}

// walkToReport drives the hide wizard from the menu to the report screen with one
// input and one carrier, using nothing but keystrokes.
func walkToReport(t *testing.T, width, height int, carrierPath, inputPath, outPath string) Model {
	t.Helper()
	m := resize(t, New("test"), width, height)
	m = press(t, m, "enter") // open Hide

	// Step 1: one input, typed into the manual path field.
	m = press(t, m, "tab")
	m = press(t, m, inputPath)
	m = press(t, m, "enter")
	m = press(t, m, "tab")
	if len(m.hide.inputs) != 1 {
		t.Fatalf("chosen inputs = %d, want 1 (note %q)", len(m.hide.inputs), m.hide.pick.note)
	}
	m = press(t, m, "enter") // to the carrier step
	if m.hide.step != stepCarrier {
		t.Fatalf("step = %v, want stepCarrier", m.hide.step)
	}

	// Step 2: the carrier.
	m = press(t, m, "tab")
	m = press(t, m, carrierPath)
	next, cmd := pressCmd(t, m, "enter")
	m = deliver(t, next, cmd)
	if m.hide.step != stepReport {
		t.Fatalf("step = %v, want stepReport", m.hide.step)
	}

	if outPath != "" {
		// Step 3: the report. The default output name would land in the working
		// directory, so point it at the test's temp dir and let the report catch up.
		m = press(t, m, "tab")
		for m.hide.output != "" {
			m = press(t, m, "backspace")
		}
		m = press(t, m, outPath)
		next, cmd = pressCmd(t, m, "tab")
		m = deliver(t, next, cmd)
	}
	return m
}

// walkToSecret continues from the report to the passphrase step.
func walkToSecret(t *testing.T, m Model) Model {
	t.Helper()
	if m.hide.planErr != nil {
		t.Fatalf("analyze failed: %v", m.hide.planErr)
	}
	m = press(t, m, "enter")
	if m.hide.step != stepSecret {
		t.Fatalf("step = %v, want stepSecret", m.hide.step)
	}
	return m
}

// revealToSecret drives the reveal wizard from the menu to its passphrase prompt,
// choosing imagePath and pointing the output at outDir. The default output is the
// working directory, which a test must not write into.
func revealToSecret(t *testing.T, imagePath, outDir string) Model {
	t.Helper()
	m := resize(t, New("test"), 80, 24)
	m = press(t, m, "down") // Reveal
	m = press(t, m, "enter")
	if m.screen != screenReveal {
		t.Fatalf("screen = %v, want screenReveal", m.screen)
	}

	// Field order: the path field, then the output directory, then the listing.
	m = press(t, m, "tab")
	m = press(t, m, "tab") // move on to the output directory
	m = pressN(t, m, "backspace", len(m.reveal.outDir))
	m = press(t, m, outDir)
	m = press(t, m, "tab") // back to the listing
	if m.reveal.outDir != outDir {
		t.Fatalf("outDir = %q, want %q", m.reveal.outDir, outDir)
	}

	m = press(t, m, "tab") // focus the path field
	m = press(t, m, imagePath)
	// Choosing the image moves to the passphrase synchronously: no command runs.
	m, _ = pressCmd(t, m, "enter")
	if m.reveal.step != revealSecret {
		t.Fatalf("step = %v, want revealSecret (note %q)", m.reveal.step, m.reveal.pick.note)
	}
	return m
}

// TestHideWizardMatchesEngine is the F2 guarantee. It runs a real hide through the
// wizard's keystrokes, reads the salt and both AEAD nonces back out of the file the
// wizard wrote, and replays the same options through internal/hide with every
// random input pinned. cmd's TestHideCLIMatchesEngine proves the CLI is that same
// engine call, so identical bytes here means the TUI and the CLI agree.
func TestHideWizardMatchesEngine(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), bytes.Repeat([]byte("wisanggeni "), 40))
	outPath := filepath.Join(dir, "tui.png")

	m := walkToSecret(t, walkToReport(t, 120, 40, carrierPath, inputPath, outPath))
	assertFits(t, m.View(), 120, 40)

	// Step 4: the passphrase twice, then run for real.
	m = press(t, m, testPassphrase)
	m = press(t, m, "tab")
	m = press(t, m, testPassphrase)
	next, cmd := pressCmd(t, m, "enter")
	if next.hide.step != stepWorking {
		t.Fatalf("step = %v, want stepWorking", next.hide.step)
	}
	m = deliver(t, next, cmd)

	if m.hide.step != stepDone {
		t.Fatalf("step = %v (err %v), want stepDone", m.hide.step, m.hide.runErr)
	}
	if !m.hide.result.Verified {
		t.Error("the wizard's output did not verify")
	}
	if m.hide.result.Output != outPath {
		t.Errorf("output = %q, want %q", m.hide.result.Output, outPath)
	}
	assertFits(t, m.View(), 120, 40)

	// The wizard must not keep the passphrase around once it has done its work.
	for i, b := range m.hide.pass {
		if b != 0 {
			t.Fatalf("passphrase byte %d survived the run", i)
		}
	}

	wizardBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read wizard output: %v", err)
	}

	written, err := carrier.Load(outPath)
	if err != nil {
		t.Fatalf("load wizard output: %v", err)
	}
	salt, err := stego.ExtractSalt(written.Image, format.SaltSize)
	if err != nil {
		t.Fatalf("extract salt: %v", err)
	}
	keys, err := crypto.Derive(testPassphrase, salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	prefix, err := reveal.ExtractAt(written.Image, keys.PosSeed, 0, reveal.PayloadPrefix)
	if err != nil {
		t.Fatalf("extract prefix: %v", err)
	}
	nonces := &hide.Nonces{
		Header: prefix[:format.NonceSize],
		Body:   prefix[format.NonceSize+format.HeaderCipherSize:],
	}

	replayPath := filepath.Join(dir, "replay.png")
	if _, err := hide.Run(hide.Options{
		Inputs:      []string{inputPath},
		CarrierPath: carrierPath,
		OutputPath:  replayPath,
		Passphrase:  []byte(testPassphrase),
		Salt:        salt,
		Nonces:      nonces,
	}); err != nil {
		t.Fatalf("engine replay: %v", err)
	}
	engineBytes, err := os.ReadFile(replayPath)
	if err != nil {
		t.Fatalf("read replay output: %v", err)
	}
	if !bytes.Equal(wizardBytes, engineBytes) {
		t.Fatalf("wizard and engine outputs differ: %d bytes vs %d", len(wizardBytes), len(engineBytes))
	}
}

// TestHideInputEnterOpensFolder guards the picker's one rule: Enter is the same
// verb in every list. On the input step it opens a folder, so a user who presses
// Enter on a folder row does not get "choose something first" - which looked
// like a broken folder - and the hint on the row says so.
func TestHideInputEnterOpensFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "docs", "FORMAT.md"), []byte("format\n"))
	writeFile(t, filepath.Join(dir, "aa.txt"), []byte("a\n"))

	m := resize(t, New("test"), 120, 40)
	m = press(t, m, "enter") // open Hide
	// Point the picker straight at the fixture: the picker opens on the package
	// directory during a test, and the manual path field adds an input rather
	// than navigating.
	m.hide.pick.dir = dir
	m.hide.pick.cursor = 0
	m.hide.pick.reload()
	m.hide.inputs = nil
	if entry, ok := m.hide.pick.current(); !ok || entry.name != "docs" {
		t.Fatalf("cursor is not on docs/: %+v (ok %v)", entry, ok)
	}

	m = press(t, m, "enter")
	if m.hide.step != stepInput {
		t.Fatalf("step = %v, want stepInput: enter on a folder must not leave the step", m.hide.step)
	}
	if got, want := m.hide.pick.dir, filepath.Join(dir, "docs"); got != want {
		t.Fatalf("picker dir = %q, want %q", got, want)
	}
	if len(m.hide.inputs) != 0 {
		t.Fatalf("opening a folder must not choose anything, got %d inputs", len(m.hide.inputs))
	}
	if m.hide.pick.note != "" {
		t.Fatalf("note after opening a folder = %q, want none", m.hide.pick.note)
	}

	// Space is still the key that chooses a folder, including the folder the user
	// is now standing in.
	m = press(t, m, " ")
	if len(m.hide.inputs) != 1 {
		t.Fatalf("chosen inputs = %d after space, want 1 (note %q)", len(m.hide.inputs), m.hide.pick.note)
	}
	m = press(t, m, "enter") // with something chosen, enter moves on
	if m.hide.step != stepCarrier {
		t.Fatalf("step = %v, want stepCarrier", m.hide.step)
	}
}

// TestHideInputEnterWithNothingChosen checks that Enter on a file before
// choosing anything explains space instead of advancing into a failure.
func TestHideInputEnterWithNothingChosen(t *testing.T) {
	dir := t.TempDir()
	m := resize(t, New("test"), 120, 40)
	m = press(t, m, "enter") // open Hide
	writeFile(t, filepath.Join(dir, "note.txt"), []byte("note\n"))
	m.hide.pick.dir = dir
	m.hide.pick.cursor = 0
	m.hide.pick.reload()

	m = press(t, m, "enter")
	if m.hide.step != stepInput {
		t.Fatalf("step = %v, want stepInput", m.hide.step)
	}
	if !strings.Contains(m.hide.pick.note, "space") {
		t.Fatalf("note = %q, want it to name the space key", m.hide.pick.note)
	}
	if view := m.View(); !strings.Contains(view, "open folder / next") {
		t.Errorf("the hint bar should say enter opens folders, got:\n%s", view)
	}
}

// TestHideWizardRejectsSmallCarrier checks that a carrier with no room fails on
// the report screen with the CLI's exit code, before any passphrase is asked for.
func TestHideWizardRejectsSmallCarrier(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "tiny.png"), noiseCarrierImage(16, 16))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), randomBytes(4096))

	m := walkToReport(t, 80, 24, carrierPath, inputPath, filepath.Join(dir, "out.png"))

	if got := exitcode.Code(m.hide.planErr); got != exitcode.Capacity {
		t.Fatalf("exit code = %d (err %v), want %d", got, m.hide.planErr, exitcode.Capacity)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"[exit 2]", "capacity is"} {
		if !strings.Contains(view, want) {
			t.Errorf("report screen missing %q, got:\n%s", want, view)
		}
	}

	// A failed report must not let Enter through to the passphrase.
	m = press(t, m, "enter")
	if m.hide.step != stepReport {
		t.Fatalf("step = %v after a failed report, want stepReport", m.hide.step)
	}
}

// TestHideWizardForceOverridesSmoothCover checks the one checkbox: a cover the
// scorer rejects is refused with exit 5, and Space retries it with force on.
func TestHideWizardForceOverridesSmoothCover(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "flat.png"), flatCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), bytes.Repeat([]byte("x"), 64))

	m := walkToReport(t, 80, 24, carrierPath, inputPath, filepath.Join(dir, "out.png"))

	if got := exitcode.Code(m.hide.planErr); got != exitcode.Unfit {
		t.Fatalf("exit code = %d (err %v), want %d", got, m.hide.planErr, exitcode.Unfit)
	}
	if view := m.View(); !strings.Contains(view, "too smooth") || !strings.Contains(view, "[exit 5]") {
		t.Errorf("report screen should name the smooth cover, got:\n%s", view)
	}

	next, cmd := pressCmd(t, m, " ")
	m = deliver(t, next, cmd)
	if !m.hide.force {
		t.Fatal("space should turn force on")
	}
	if m.hide.planErr != nil {
		t.Fatalf("with force the carrier should be accepted, got %v", m.hide.planErr)
	}
	m = press(t, m, "enter")
	if m.hide.step != stepSecret {
		t.Fatalf("step = %v, want stepSecret", m.hide.step)
	}
}

// TestHideWizardFillNeedsForce checks the middle fill band: above 10% the job is
// refused until force is on.
func TestHideWizardFillNeedsForce(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "payload.bin"), randomBytes(6000))

	m := walkToReport(t, 80, 24, carrierPath, inputPath, filepath.Join(dir, "out.png"))

	if got := exitcode.Code(m.hide.planErr); got != exitcode.Unfit {
		t.Fatalf("exit code = %d (err %v), want %d", got, m.hide.planErr, exitcode.Unfit)
	}
	if view := m.View(); !strings.Contains(view, "use --force") {
		t.Errorf("report screen should ask for force, got:\n%s", view)
	}

	next, cmd := pressCmd(t, m, " ")
	m = deliver(t, next, cmd)
	if m.hide.planErr != nil {
		t.Fatalf("with force the fill should be accepted, got %v", m.hide.planErr)
	}
}

// TestHideWizardBlocksBadPassphrase checks the two-field guard: nothing runs
// until the passphrase is present and typed the same way twice.
func TestHideWizardBlocksBadPassphrase(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), bytes.Repeat([]byte("x"), 64))

	m := walkToSecret(t, walkToReport(t, 80, 24, carrierPath, inputPath, filepath.Join(dir, "out.png")))

	m = press(t, m, "enter")
	if m.hide.step != stepSecret {
		t.Fatal("an empty passphrase must not start the job")
	}
	if !strings.Contains(m.View(), "cannot be empty") {
		t.Errorf("expected an empty-passphrase note, got:\n%s", m.View())
	}

	m = press(t, m, "a-long-enough-secret")
	m = press(t, m, "tab")
	m = press(t, m, "a-different-secret")
	m = press(t, m, "enter")
	if m.hide.step != stepSecret {
		t.Fatal("a mismatched confirmation must not start the job")
	}
	if !strings.Contains(m.View(), "do not match") {
		t.Errorf("expected a mismatch note, got:\n%s", m.View())
	}
}

// TestRevealWizardRoundTrip hides a payload with the engine and reads it back
// through the wizard's keystrokes.
func TestRevealWizardRoundTrip(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), bytes.Repeat([]byte("wisanggeni "), 40))
	imagePath := filepath.Join(dir, "hidden.png")
	if _, err := hide.Run(hide.Options{
		Inputs:      []string{inputPath},
		CarrierPath: carrierPath,
		OutputPath:  imagePath,
		Passphrase:  []byte(testPassphrase),
	}); err != nil {
		t.Fatalf("engine hide: %v", err)
	}

	outDir := t.TempDir()
	m := revealToSecret(t, imagePath, outDir)
	m = press(t, m, testPassphrase)
	next, cmd := pressCmd(t, m, "enter")
	if next.reveal.step != revealWorking {
		t.Fatalf("step = %v, want revealWorking", next.reveal.step)
	}
	m = deliver(t, next, cmd)

	if m.reveal.step != revealDone {
		t.Fatalf("step = %v (err %v), want revealDone", m.reveal.step, m.reveal.err)
	}
	if len(m.reveal.result.Entries) == 0 {
		t.Fatal("no entries reported")
	}
	if _, err := os.Stat(filepath.Join(outDir, "note.txt")); err != nil {
		t.Fatalf("the payload was not written: %v", err)
	}
	assertFits(t, m.View(), 80, 24)
	if !strings.Contains(m.View(), "note.txt") {
		t.Errorf("done screen should list the entries, got:\n%s", m.View())
	}
	// The passphrase must not survive the run.
	for i, b := range m.reveal.pass {
		if b != 0 {
			t.Fatalf("passphrase byte %d survived the run", i)
		}
	}
}

// TestRevealWizardWrongPassphrase checks that a bad passphrase returns to the
// prompt with the CLI's generic message and exit code.
func TestRevealWizardWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), bytes.Repeat([]byte("x"), 64))
	imagePath := filepath.Join(dir, "hidden.png")
	if _, err := hide.Run(hide.Options{
		Inputs:      []string{inputPath},
		CarrierPath: carrierPath,
		OutputPath:  imagePath,
		Passphrase:  []byte(testPassphrase),
	}); err != nil {
		t.Fatalf("engine hide: %v", err)
	}

	m := revealToSecret(t, imagePath, t.TempDir())
	m = press(t, m, "not-the-passphrase")
	next, cmd := pressCmd(t, m, "enter")
	m = deliver(t, next, cmd)

	if m.reveal.step != revealSecret {
		t.Fatalf("step = %v, want to be back at the prompt", m.reveal.step)
	}
	if got := exitcode.Code(m.reveal.err); got != exitcode.Decrypt {
		t.Fatalf("exit code = %d (err %v), want %d", got, m.reveal.err, exitcode.Decrypt)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"[exit 3]", "wrong passphrase"} {
		if !strings.Contains(view, want) {
			t.Errorf("failure screen missing %q, got:\n%s", want, view)
		}
	}
}

// TestWizardScreensFitSmallTerminal renders every screen of both flows at 80x24,
// data included: a wrap or an overflow is a bug that a 120x40 golden path would
// not catch, and the report screen is the one with the most rows.
func TestWizardScreensFitSmallTerminal(t *testing.T) {
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "note.txt"), bytes.Repeat([]byte("x"), 64))

	m := walkToReport(t, 80, 24, carrierPath, inputPath, filepath.Join(dir, "out.png"))
	assertFits(t, m.View(), 80, 24)
	m = walkToSecret(t, m)
	assertFits(t, m.View(), 80, 24)

	// Working and Done need no extra run to be rendered: they read the same state.
	for _, step := range []hideStep{stepInput, stepCarrier, stepReport, stepWorking, stepDone} {
		m.hide.step = step
		assertFits(t, m.View(), 80, 24)
	}

	m.reveal.image = filepath.Join(dir, "hidden.png")
	m.screen = screenReveal
	for _, step := range []revealStep{revealPick, revealSecret, revealWorking} {
		m.reveal.step = step
		assertFits(t, m.View(), 80, 24)
	}
	m.reveal.step = revealDone
	m.reveal.result = reveal.Result{
		Dir:     dir,
		Entries: []archive.Entry{{Name: "note.txt", Size: 1024}},
		Total:   1024,
	}
	assertFits(t, m.View(), 80, 24)
}
