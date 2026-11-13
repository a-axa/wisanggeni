package cmd

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/prompt"
	"github.com/a-axa/wisanggeni/internal/stego"
)

func runInspectCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := newInspectCmd()
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	c.SilenceErrors = true
	c.SilenceUsage = true
	err = c.Execute()
	return out.String(), errOut.String(), err
}

// noPrompt forces the non-interactive path, so a test run never blocks on the
// terminal of whoever started it.
func noPrompt(t *testing.T) {
	t.Helper()
	orig := promptAvailable
	promptAvailable = func() bool { return false }
	t.Cleanup(func() { promptAvailable = orig })
}

// inspectStego hides an already packed payload in a fresh image, keeping the
// header flag in step with how Pack compressed it.
func inspectStego(t *testing.T, carrierPath, pass string, packed archive.Packed) string {
	t.Helper()
	flags := uint8(0)
	if packed.Compressed {
		flags = format.FlagZstd
	}
	path := filepath.Join(t.TempDir(), "stego.png")
	embedBlobAt(t, carrierPath, path, pass, flags, packed.Data)
	return path
}

// packDir arranges files under a folder and packs it, as hide would.
func packDir(t *testing.T, root string, files map[string][]byte) archive.Packed {
	t.Helper()
	return packDirWith(t, root, files, archive.Options{})
}

// packDirWith is packDir with explicit options, for the tests that need to pin
// compression on or off.
func packDirWith(t *testing.T, root string, files map[string][]byte, opts archive.Options) archive.Packed {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeBytes(t, path, content)
	}
	packed, err := archive.Pack([]string{root}, opts)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return packed
}

// TestInspectWithoutPassphrase pins §7.10: with no passphrase the command reports
// the carrier and nothing else. The absence of the payload words is the point.
func TestInspectWithoutPassphrase(t *testing.T) {
	noPrompt(t)
	path := saveImage(t, noiseImage(256, 256))

	// Recompute the capacity the way capacity does, so the assertion follows the
	// carrier rather than a hardcoded number.
	car, err := carrier.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	width, height := car.Image.Rect.Dx(), car.Image.Rect.Dy()

	stdout, stderr, err := runInspectCLI(t, path)
	if err != nil {
		t.Fatalf("inspect: %v (stderr %s)", err, stderr)
	}

	for _, w := range []string{
		"dimensions    256x256",
		fmt.Sprintf("usable        %d bytes", stego.Capacity(width, height)),
		"cover         suitable",
		"alpha         no",
		"chunks        none",
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("inspect output lacks %q:\n%s", w, stdout)
		}
	}

	for _, leak := range []string{"payload", "version", "body", "entries", "ratio", "total"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("carrier-only output leaks %q:\n%s", leak, stdout)
		}
	}
}

func TestInspectHeaderOnly(t *testing.T) {
	noPrompt(t)
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))
	packed := packDir(t, filepath.Join(t.TempDir(), "project"), map[string][]byte{
		"a.txt": []byte("alpha"),
	})
	stegoPath := inspectStego(t, carrierPath, pass, packed)

	stdout, stderr, err := runInspectCLI(t, stegoPath, "--header-only", "--passphrase-file", passFile(t, pass))
	if err != nil {
		t.Fatalf("inspect --header-only: %v (stderr %s)", err, stderr)
	}

	for _, w := range []string{
		"payload       header only",
		"version       1",
		fmt.Sprintf("body          %d bytes", len(packed.Data)),
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("inspect output lacks %q:\n%s", w, stdout)
		}
	}
	// The archive is never parsed, so no member name may appear.
	for _, leak := range []string{"entries", "ratio", "a.txt", "total"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("header-only output leaks %q:\n%s", leak, stdout)
		}
	}
}

func TestInspectFullListsEntriesWithoutWriting(t *testing.T) {
	noPrompt(t)
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))

	// A distinctive root name: if inspect ever unpacked, the check at the end
	// would find this directory in the working directory.
	root := filepath.Join(t.TempDir(), "inspect-must-not-write")
	packed := packDir(t, root, map[string][]byte{
		"a.txt":        []byte("alpha"),
		"nested/b.txt": []byte("beta gamma"),
	})
	stegoPath := inspectStego(t, carrierPath, pass, packed)

	stdout, stderr, err := runInspectCLI(t, stegoPath, "--passphrase-file", passFile(t, pass))
	if err != nil {
		t.Fatalf("inspect: %v (stderr %s)", err, stderr)
	}

	for _, w := range []string{
		"payload       decrypted",
		"version       1",
		"entries       4 (2 files)",
		"  inspect-must-not-write (directory)",
		"  inspect-must-not-write/a.txt  5 bytes",
		"  inspect-must-not-write/nested (directory)",
		"  inspect-must-not-write/nested/b.txt  10 bytes",
		"total         15 bytes uncompressed",
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("inspect output lacks %q:\n%s", w, stdout)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "inspect-must-not-write")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("inspect wrote the payload to disk (%v)", err)
	}
}

// TestInspectReportsCompression covers a zstd payload: the flag line says so and
// a size ratio is meaningful, unlike for a raw tar.
func TestInspectReportsCompression(t *testing.T) {
	noPrompt(t)
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))
	packed := packDir(t, filepath.Join(t.TempDir(), "rep"), map[string][]byte{
		"rep.txt": bytes.Repeat([]byte("wisanggeni "), 256),
	})
	if !packed.Compressed {
		t.Fatal("the repetitive payload was not compressed, so this test proves nothing")
	}
	stegoPath := inspectStego(t, carrierPath, pass, packed)

	stdout, stderr, err := runInspectCLI(t, stegoPath, "--passphrase-file", passFile(t, pass))
	if err != nil {
		t.Fatalf("inspect: %v (stderr %s)", err, stderr)
	}
	for _, w := range []string{"flags         zstd", "ratio         "} {
		if !strings.Contains(stdout, w) {
			t.Errorf("inspect output lacks %q:\n%s", w, stdout)
		}
	}
}

// TestInspectKeepsCompressionOffWhenPackDid covers the other branch of the flag
// line: no zstd means no ratio line, since the tar overhead is not content.
func TestInspectReportsNoCompression(t *testing.T) {
	noPrompt(t)
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))
	// Incompressible content, and compression switched off, so Pack keeps the raw
	// tar whatever zstd would have done with the padding.
	bin := make([]byte, 3000)
	if _, err := rand.Read(bin); err != nil {
		t.Fatalf("rand: %v", err)
	}
	packed := packDirWith(t, filepath.Join(t.TempDir(), "raw"), map[string][]byte{
		"bin": bin,
	}, archive.Options{NoCompress: true})
	if packed.Compressed {
		t.Fatal("the payload compressed, so this test proves nothing")
	}
	stegoPath := inspectStego(t, carrierPath, pass, packed)

	stdout, stderr, err := runInspectCLI(t, stegoPath, "--passphrase-file", passFile(t, pass))
	if err != nil {
		t.Fatalf("inspect: %v (stderr %s)", err, stderr)
	}
	if !strings.Contains(stdout, "flags         none") {
		t.Errorf("inspect output does not report an uncompressed payload:\n%s", stdout)
	}
	if strings.Contains(stdout, "ratio") {
		t.Errorf("inspect invented a ratio for a raw tar:\n%s", stdout)
	}
}

func TestInspectWrongPassphrase(t *testing.T) {
	noPrompt(t)
	carrierPath := saveImage(t, noiseImage(256, 256))
	packed := packDir(t, filepath.Join(t.TempDir(), "project"), map[string][]byte{
		"a.txt": []byte("alpha"),
	})
	stegoPath := inspectStego(t, carrierPath, "correct horse battery staple", packed)

	stdout, _, err := runInspectCLI(t, stegoPath, "--passphrase-file", passFile(t, "wrong passphrase entirely"))
	if codeOf(err) != ExitDecrypt {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitDecrypt)
	}
	// The message must stay generic, exactly as reveal's does.
	if !strings.Contains(err.Error(), "cannot decrypt") {
		t.Fatalf("error %q is not the generic decrypt message", err)
	}
	if strings.Contains(stdout, "payload") {
		t.Errorf("a failed decrypt still reported a payload layer:\n%s", stdout)
	}
}

func TestInspectPlainImageHasNoData(t *testing.T) {
	noPrompt(t)
	path := saveImage(t, noiseImage(256, 256))

	_, _, err := runInspectCLI(t, path, "--passphrase-file", passFile(t, "correct horse battery staple"))
	if codeOf(err) != ExitDecrypt {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitDecrypt)
	}
}

func TestInspectHeaderOnlyNeedsPassphrase(t *testing.T) {
	noPrompt(t)
	path := saveImage(t, noiseImage(64, 64))

	_, _, err := runInspectCLI(t, path, "--header-only")
	if codeOf(err) != ExitError {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitError)
	}
	if !strings.Contains(err.Error(), "needs a passphrase") {
		t.Fatalf("error %q does not explain the missing passphrase", err)
	}
}

// TestInspectEmptyPromptSkipsPayload pins the interactive rule: an empty answer
// asks for the carrier layer, which is not a failure as it is in hide and reveal.
func TestInspectEmptyPromptSkipsPayload(t *testing.T) {
	origAvail, origPrompt := promptAvailable, promptPassphrase
	t.Cleanup(func() { promptAvailable, promptPassphrase = origAvail, origPrompt })
	promptAvailable = func() bool { return true }
	promptPassphrase = func() (prompt.Result, error) { return prompt.Result{}, prompt.ErrEmpty }

	path := saveImage(t, noiseImage(64, 64))

	stdout, stderr, err := runInspectCLI(t, path)
	if err != nil {
		t.Fatalf("inspect: %v (stderr %s)", err, stderr)
	}
	if strings.Contains(stdout, "payload") {
		t.Errorf("an empty answer still produced a payload layer:\n%s", stdout)
	}
}

// TestInspectShortPassphraseWarns keeps the advisory note from hiding.
func TestInspectShortPassphraseWarns(t *testing.T) {
	noPrompt(t)
	pass := "short"
	carrierPath := saveImage(t, noiseImage(256, 256))
	packed := packDir(t, filepath.Join(t.TempDir(), "project"), map[string][]byte{
		"a.txt": []byte("alpha"),
	})
	stegoPath := inspectStego(t, carrierPath, pass, packed)

	_, stderr, err := runInspectCLI(t, stegoPath, "--passphrase-file", passFile(t, pass))
	if err != nil {
		t.Fatalf("inspect: %v (stderr %s)", err, stderr)
	}
	if !strings.Contains(stderr, "shorter than") {
		t.Errorf("stderr = %q, want a length warning", stderr)
	}
}

func TestInspectAlphaCarrier(t *testing.T) {
	noPrompt(t)
	stdout, stderr, err := runInspectCLI(t, writeAlphaPNG(t, 40, 30))
	if err != nil {
		t.Fatalf("inspect on an alpha carrier: %v (stderr %s)", err, stderr)
	}
	for _, w := range []string{"dimensions    40x30", "alpha         yes", "cover         not scored"} {
		if !strings.Contains(stdout, w) {
			t.Errorf("inspect output lacks %q:\n%s", w, stdout)
		}
	}
}
