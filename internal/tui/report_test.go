package tui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/hide"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// menuScore, menuCapacity and menuInspect are the menu indices of the three
// reporting screens, in the order menuItems lists them.
const (
	menuScore    = 2
	menuCapacity = 3
	menuInspect  = 4
)

// openScreen enters the menu item at index item and chooses path through the
// picker's manual path field, then runs the load command. It is the shared first
// move of the Score, Capacity and Inspect screens.
func openScreen(t *testing.T, width, height, item int, path string) Model {
	t.Helper()
	m := resize(t, New("test"), width, height)
	for range item {
		m = press(t, m, "down")
	}
	m = press(t, m, "enter")
	m = press(t, m, "tab")
	m = press(t, m, path)
	next, cmd := pressCmd(t, m, "enter")
	return deliver(t, next, cmd)
}

// alphaCarrier saves an otherwise ordinary image with one translucent pixel. The
// engine refuses such a carrier, so this is the fixture that proves the report
// screens still describe it.
func alphaCarrier(t *testing.T, path string, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 100, 50, 0xff
	}
	img.Pix[3] = 0x80

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create alpha PNG: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode alpha PNG: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close alpha PNG: %v", err)
	}
	return path
}

// stegoPayload hides a distinctively named file in a fresh carrier and returns
// the path of the stego PNG.
func stegoPayload(t *testing.T, pass string) string {
	t.Helper()
	dir := t.TempDir()
	carrierPath := writeCarrier(t, filepath.Join(dir, "carrier.png"), noiseCarrierImage(256, 256))
	inputPath := writeFile(t, filepath.Join(dir, "inspect-secret.txt"), []byte("a distinct payload"))
	imagePath := filepath.Join(dir, "hidden.png")
	if _, err := hide.Run(hide.Options{
		Inputs:      []string{inputPath},
		CarrierPath: carrierPath,
		OutputPath:  imagePath,
		Passphrase:  []byte(pass),
	}); err != nil {
		t.Fatalf("engine hide: %v", err)
	}
	return imagePath
}

func TestScoreScreenShowsMeasuredCarrier(t *testing.T) {
	path := writeCarrier(t, filepath.Join(t.TempDir(), "cover.png"), noiseCarrierImage(64, 64))
	m := openScreen(t, 80, 24, menuScore, path)

	if m.report.step != reportShow {
		t.Fatalf("step = %v, want reportShow", m.report.step)
	}
	if m.report.err != nil {
		t.Fatalf("report failed: %v", m.report.err)
	}
	if m.report.info == nil {
		t.Fatal("no carrier info was measured")
	}

	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{
		"dimensions", "64x64",
		fmt.Sprintf("%d", stego.Channels(64, 64)),
		"textured_fraction", "median_rho", "suitable",
		fmt.Sprintf("%d bytes", stego.CapacityRaw(64, 64)),
		fmt.Sprintf("%d bytes", stego.Capacity(64, 64)),
		fmt.Sprintf("%d bytes", stego.SuggestedCapacity(64, 64)),
		fmt.Sprintf("%d bytes", stego.StrongCapacity(64, 64)),
		"Nothing is written",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("score view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Not available in this build yet") {
		t.Errorf("score screen is still the placeholder:\n%s", view)
	}
}

// TestCapacityScreenIsItsOwnMode pins that the Capacity entry opens the same
// report under the capacity heading.
func TestCapacityScreenIsItsOwnMode(t *testing.T) {
	path := writeCarrier(t, filepath.Join(t.TempDir(), "cover.png"), noiseCarrierImage(64, 64))
	m := openScreen(t, 80, 24, menuCapacity, path)

	if m.screen != screenCapacity {
		t.Fatalf("screen = %v, want screenCapacity", m.screen)
	}
	if m.report.mode != modeCapacity {
		t.Fatalf("mode = %v, want modeCapacity", m.report.mode)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"Capacity", "dimensions", "usable"} {
		if !strings.Contains(view, want) {
			t.Errorf("capacity view missing %q:\n%s", want, view)
		}
	}
}

func TestScoreScreenRejectsFlatCover(t *testing.T) {
	path := writeCarrier(t, filepath.Join(t.TempDir(), "flat.png"), flatCarrierImage(64, 64))
	m := openScreen(t, 80, 24, menuScore, path)
	if m.report.err != nil {
		t.Fatalf("report failed: %v", m.report.err)
	}
	if !strings.Contains(m.View(), "reject") {
		t.Errorf("a flat cover should be rejected:\n%s", m.View())
	}
}

// TestScoreScreenDescribesAlphaCarrier proves the report does not fail on a
// carrier the engine would refuse: it says why it cannot be used.
func TestScoreScreenDescribesAlphaCarrier(t *testing.T) {
	path := alphaCarrier(t, filepath.Join(t.TempDir(), "alpha.png"), 40, 30)
	m := openScreen(t, 80, 24, menuScore, path)
	if m.report.err != nil {
		t.Fatalf("a transparent carrier should still be reported: %v", m.report.err)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"40x30", "alpha", "cannot be used"} {
		if !strings.Contains(view, want) {
			t.Errorf("alpha report missing %q:\n%s", want, view)
		}
	}
}

func TestReportScreenUnreadableFileShowsTheError(t *testing.T) {
	path := writeFile(t, filepath.Join(t.TempDir(), "broken.png"), []byte("not a PNG at all"))
	m := openScreen(t, 80, 24, menuScore, path)
	if m.report.err == nil {
		t.Fatal("a corrupt PNG should fail to load")
	}
	if code := exitcode.Code(m.report.err); code != exitcode.Error {
		t.Errorf("exit = %d, want %d", code, exitcode.Error)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	if !strings.Contains(view, "[exit 1]") {
		t.Errorf("the failure row lost its exit code:\n%s", view)
	}
}

func TestReportScreenBackGoesToListThenMenu(t *testing.T) {
	path := writeCarrier(t, filepath.Join(t.TempDir(), "cover.png"), noiseCarrierImage(64, 64))
	m := openScreen(t, 80, 24, menuScore, path)

	m = press(t, m, "esc")
	if m.screen != screenScore || m.report.step != reportPick {
		t.Fatalf("esc from the report: screen %v step %v, want score/reportPick", m.screen, m.report.step)
	}
	m = press(t, m, "esc")
	if m.screen != screenMenu {
		t.Fatalf("second esc: screen %v, want screenMenu", m.screen)
	}
}

func TestReportScreenFitsWideTerminal(t *testing.T) {
	path := writeCarrier(t, filepath.Join(t.TempDir(), "cover.png"), noiseCarrierImage(256, 256))
	m := openScreen(t, 120, 40, menuScore, path)
	assertFits(t, m.View(), 120, 40)
}

// TestInspectWithoutPassphraseLeaksNothing pins §7.10: the carrier layer is all
// that appears until a passphrase opens the blob.
func TestInspectWithoutPassphraseLeaksNothing(t *testing.T) {
	imagePath := stegoPayload(t, testPassphrase)
	m := openScreen(t, 80, 24, menuInspect, imagePath)

	if m.inspector.step != inspectReport {
		t.Fatalf("step = %v, want inspectReport", m.inspector.step)
	}
	if m.inspector.err != nil {
		t.Fatalf("carrier read failed: %v", m.inspector.err)
	}
	if m.inspector.payload != nil {
		t.Fatal("a payload appeared before any passphrase")
	}

	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"dimensions", "256x256", "capacity raw", "carrier layer only"} {
		if !strings.Contains(view, want) {
			t.Errorf("inspect view missing %q:\n%s", want, view)
		}
	}
	for _, leak := range []string{"inspect-secret", "entries", "version", "body", "decrypted"} {
		if strings.Contains(view, leak) {
			t.Errorf("carrier-only view leaks %q:\n%s", leak, view)
		}
	}
}

// TestInspectWithPassphraseListsEntries covers the full decrypt: the entry list
// appears and the passphrase is dropped as soon as it has done its work.
func TestInspectWithPassphraseListsEntries(t *testing.T) {
	imagePath := stegoPayload(t, testPassphrase)
	m := openScreen(t, 80, 24, menuInspect, imagePath)

	m = press(t, m, testPassphrase)
	next, cmd := pressCmd(t, m, "enter")
	if next.inspector.step != inspectWorking {
		t.Fatalf("step = %v, want inspectWorking", next.inspector.step)
	}
	m = deliver(t, next, cmd)

	if m.inspector.step != inspectPayload {
		t.Fatalf("step = %v (err %v), want inspectPayload", m.inspector.step, m.inspector.err)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"inspect-secret.txt", "decrypted", "entries", "version", "writes nothing to disk"} {
		if !strings.Contains(view, want) {
			t.Errorf("payload view missing %q:\n%s", want, view)
		}
	}
	for i, b := range m.inspector.pass {
		if b != 0 {
			t.Fatalf("passphrase byte %d survived the decrypt", i)
		}
	}
}

// TestInspectWrongPassphraseIsNotAnOracle keeps the TUI error identical to the
// CLI's: the same generic message and the same exit code.
func TestInspectWrongPassphraseIsNotAnOracle(t *testing.T) {
	imagePath := stegoPayload(t, testPassphrase)
	m := openScreen(t, 80, 24, menuInspect, imagePath)

	m = press(t, m, "not-the-passphrase")
	next, cmd := pressCmd(t, m, "enter")
	m = deliver(t, next, cmd)

	if m.inspector.step != inspectReport {
		t.Fatalf("step = %v, want to be back at the passphrase field", m.inspector.step)
	}
	if got := exitcode.Code(m.inspector.err); got != exitcode.Decrypt {
		t.Fatalf("exit code = %d (err %v), want %d", got, m.inspector.err, exitcode.Decrypt)
	}
	view := m.View()
	assertFits(t, view, 80, 24)
	for _, want := range []string{"[exit 3]", "cannot decrypt"} {
		if !strings.Contains(view, want) {
			t.Errorf("failure view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "inspect-secret") {
		t.Errorf("a failed decrypt still leaked the entry list:\n%s", view)
	}
}

// TestInspectEmptyPassphraseKeepsTheCarrierLayer pins the one rule that differs
// from hide and reveal: at inspect, an empty answer is not a failure.
func TestInspectEmptyPassphraseKeepsTheCarrierLayer(t *testing.T) {
	imagePath := stegoPayload(t, testPassphrase)
	m := openScreen(t, 80, 24, menuInspect, imagePath)

	next, cmd := pressCmd(t, m, "enter")
	if cmd != nil {
		t.Fatal("an empty passphrase should not start a decrypt")
	}
	if next.inspector.step != inspectReport {
		t.Fatalf("step = %v, want inspectReport", next.inspector.step)
	}
	if next.inspector.note == "" {
		t.Error("an empty answer should explain what a passphrase would do")
	}
	if strings.Contains(next.View(), "inspect-secret") {
		t.Errorf("the carrier layer leaked payload data:\n%s", next.View())
	}
}

func TestInspectPayloadScreenFitsWideTerminal(t *testing.T) {
	imagePath := stegoPayload(t, testPassphrase)
	m := openScreen(t, 120, 40, menuInspect, imagePath)
	m = press(t, m, testPassphrase)
	next, cmd := pressCmd(t, m, "enter")
	m = deliver(t, next, cmd)
	assertFits(t, m.View(), 120, 40)
}

// TestInspectPayloadReturnsToTheMenu checks the last screen's exit key.
func TestInspectPayloadReturnsToTheMenu(t *testing.T) {
	imagePath := stegoPayload(t, testPassphrase)
	m := openScreen(t, 80, 24, menuInspect, imagePath)
	m = press(t, m, testPassphrase)
	next, cmd := pressCmd(t, m, "enter")
	m = deliver(t, next, cmd)

	m = press(t, m, "enter")
	if m.screen != screenMenu {
		t.Fatalf("screen = %v, want screenMenu", m.screen)
	}
}
