package cmd

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/carrier"
)

// saveBMPImage writes a carrier as BMP, so the CLI tests below exercise format
// detection and the BMP writer instead of only the PNG path.
func saveBMPImage(t *testing.T, img *image.NRGBA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "carrier.bmp")
	if err := carrier.Save(path, img, nil); err != nil {
		t.Fatalf("save BMP carrier: %v", err)
	}
	return path
}

// TestCLIBMPRoundTrip is the CLI-level happy path for a BMP carrier and a BMP
// output: no new flags, the format is inferred from the extensions.
func TestCLIBMPRoundTrip(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveBMPImage(t, noiseImage(256, 256))
	payload := filepath.Join(dir, "secret.txt")
	body := []byte(strings.Repeat("wisanggeni bmp ", 40))
	writeBytes(t, payload, body)
	stegoPath := filepath.Join(dir, "out.bmp")

	got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", stegoPath, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("hide exit %d, want 0\nstdout: %s\nstderr: %s", got.code, got.stdout, got.stderr)
	}

	// The output must be a real BMP, and it must differ from the carrier.
	raw := readImageBytes(t, stegoPath)
	if len(raw) < 2 || raw[0] != 'B' || raw[1] != 'M' {
		t.Fatalf("output is not a BMP: % x", raw[:min(4, len(raw))])
	}
	if bytes.Equal(raw, readImageBytes(t, carrierPath)) {
		t.Fatal("output is byte-identical to the carrier")
	}

	revealDir := filepath.Join(dir, "revealed")
	got = runBinary(t, e2ePass+"\n", "reveal", stegoPath, "-o", revealDir, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("reveal exit %d, want 0\nstdout: %s\nstderr: %s", got.code, got.stdout, got.stderr)
	}
	var found string
	_ = filepath.WalkDir(revealDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == "secret.txt" {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("revealed secret.txt not found under %s", revealDir)
	}
	assertFileEquals(t, found, body)
}

// TestCLIBMPSniffIgnoresExtension pins the "content, not name" rule: a BMP that
// is named .png must still be read as a BMP.
func TestCLIBMPSniffIgnoresExtension(t *testing.T) {
	dir := t.TempDir()
	// Write a real BMP, then rename it to .png: the loader must go by content.
	realPath := filepath.Join(dir, "carrier.bmp")
	if err := carrier.Save(realPath, noiseImage(256, 256), nil); err != nil {
		t.Fatalf("save BMP: %v", err)
	}
	misnamed := filepath.Join(dir, "carrier.png")
	if err := os.Rename(realPath, misnamed); err != nil {
		t.Fatalf("rename: %v", err)
	}
	raw, err := os.ReadFile(misnamed)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(raw) < 2 || raw[0] != 'B' || raw[1] != 'M' {
		t.Fatalf("saved file is not a BMP: % x", raw[:min(4, len(raw))])
	}

	payload := filepath.Join(dir, "secret.txt")
	writeBytes(t, payload, []byte(strings.Repeat("wisanggeni ", 40)))
	stegoPath := filepath.Join(dir, "out.bmp")

	got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", misnamed, "-o", stegoPath, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("hide exit %d, want 0; a BMP named .png must still be sniffed\nstderr: %s",
			got.code, got.stderr)
	}
}

// TestCLIBMPCapacityAndScore runs the two report commands on a BMP input.
func TestCLIBMPCapacityAndScore(t *testing.T) {
	path := saveBMPImage(t, noiseImage(256, 256))

	stdout, stderr, err := runCapacityCLI(t, path)
	if err != nil {
		t.Fatalf("capacity: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "dimensions    256x256") {
		t.Errorf("capacity stdout missing dimensions line:\n%s", stdout)
	}
	// A BMP has no ancillary chunks, so the chunks line must say none.
	if !strings.Contains(stdout, "chunks        none") {
		t.Errorf("capacity stdout missing 'chunks none' for a BMP:\n%s", stdout)
	}

	stdout, stderr, err = runScoreCLI(t, path)
	if err != nil {
		t.Fatalf("score: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "256x256") {
		t.Errorf("score stdout missing dimensions:\n%s", stdout)
	}
}
