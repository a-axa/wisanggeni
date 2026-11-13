package cmd

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// runRevealCLI drives the real command the way the binary would.
func runRevealCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := newRevealCmd()
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	c.SilenceErrors = true
	c.SilenceUsage = true
	err = c.Execute()
	return out.String(), errOut.String(), err
}

// tarOf builds a one-file tar archive, standing in for what Pack produces.
func tarOf(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

// embedBlobAt seals plain into a stego image and saves it, bypassing the CLI so
// a test can build an image hide itself would refuse to write.
func embedBlobAt(t *testing.T, carrierPath, outPath, pass string, flags uint8, plain []byte) {
	t.Helper()
	car, err := carrier.Load(carrierPath)
	if err != nil {
		t.Fatalf("load carrier: %v", err)
	}
	salt := make([]byte, format.SaltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("rand: %v", err)
	}
	keys, err := crypto.Derive(pass, salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	blob, err := crypto.Seal(keys.EncKey, salt, flags, plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := stego.EmbedBlob(car.Image, blob, keys.PosSeed, stego.CostMap(car.Image)); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if err := carrier.Save(outPath, car.Image, car.Chunks); err != nil {
		t.Fatalf("save stego image: %v", err)
	}
}

func assertFileEquals(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%q differs from the input (%d bytes vs %d)", path, len(got), len(want))
	}
}

// hideAndReveal hides inputs into a fresh output and reveals them again into a
// fresh directory, returning that directory.
func hideAndReveal(t *testing.T, carrierPath string, inputs []string, pass string, hideFlags, revealFlags []string) string {
	t.Helper()
	stegoPath := filepath.Join(t.TempDir(), "stego.png")
	passPath := passFile(t, pass)

	hideArgs := append(append([]string{}, inputs...),
		"-c", carrierPath, "-o", stegoPath, "--passphrase-file", passPath)
	hideArgs = append(hideArgs, hideFlags...)
	if _, stderr, err := runHideCLI(t, hideArgs...); err != nil {
		t.Fatalf("hide: %v (stderr %s)", err, stderr)
	}

	dir := t.TempDir()
	revealArgs := append([]string{stegoPath, "-o", dir, "--passphrase-file", passPath}, revealFlags...)
	stdout, stderr, err := runRevealCLI(t, revealArgs...)
	if err != nil {
		t.Fatalf("reveal: %v (stderr %s)", err, stderr)
	}
	if !strings.Contains(stdout, "revealed") {
		t.Fatalf("stdout = %q, want a reveal notice", stdout)
	}
	return dir
}

func TestRevealRoundTrip(t *testing.T) {
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))

	t.Run("1KB", func(t *testing.T) {
		content := make([]byte, 1024)
		if _, err := rand.Read(content); err != nil {
			t.Fatalf("rand: %v", err)
		}
		input := filepath.Join(t.TempDir(), "note.bin")
		writeBytes(t, input, content)

		dir := hideAndReveal(t, carrierPath, []string{input}, pass, nil, nil)
		assertFileEquals(t, filepath.Join(dir, "note.bin"), content)
	})

	t.Run("1MB-compressible", func(t *testing.T) {
		content := bytes.Repeat([]byte("wisanggeni "), 102400) // 1 MiB
		input := filepath.Join(t.TempDir(), "big.txt")
		writeBytes(t, input, content)

		dir := hideAndReveal(t, carrierPath, []string{input}, pass, nil, nil)
		assertFileEquals(t, filepath.Join(dir, "big.txt"), content)
	})

	t.Run("90-percent-capacity", func(t *testing.T) {
		// The STC rate of 1/2 puts the CLI fill limit at half of Capacity, so a
		// payload this dense needs --force; the point here is the reader. A
		// larger carrier keeps the tar padding from eating the budget.
		denseCarrier := saveImage(t, noiseImage(512, 512))
		capacity := stego.Capacity(512, 512)
		content := make([]byte, capacity*9/10)
		if _, err := rand.Read(content); err != nil {
			t.Fatalf("rand: %v", err)
		}
		input := filepath.Join(t.TempDir(), "dense.bin")
		writeBytes(t, input, content)

		dir := hideAndReveal(t, denseCarrier, []string{input}, pass,
			[]string{"--no-compress", "--force"}, nil)
		assertFileEquals(t, filepath.Join(dir, "dense.bin"), content)
	})

	t.Run("folder", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "project")
		if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeBytes(t, filepath.Join(src, "a.txt"), []byte("alpha"))
		nested := []byte("beta gamma delta")
		writeBytes(t, filepath.Join(src, "nested", "b.txt"), nested)

		dir := hideAndReveal(t, carrierPath, []string{src}, pass, nil, nil)
		assertFileEquals(t, filepath.Join(dir, "project", "a.txt"), []byte("alpha"))
		assertFileEquals(t, filepath.Join(dir, "project", "nested", "b.txt"), nested)
	})
}

func TestRevealWrongPassphrase(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(256, 256))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("the colonel's secret recipe"))

	stegoPath := filepath.Join(t.TempDir(), "stego.png")
	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", stegoPath,
		"--passphrase-file", passFile(t, "correct horse battery staple")); err != nil {
		t.Fatalf("hide: %v (stderr %s)", err, stderr)
	}

	dir := t.TempDir()
	_, _, err := runRevealCLI(t, stegoPath, "-o", dir, "--passphrase-file", passFile(t, "wrong passphrase entirely"))
	if codeOf(err) != ExitDecrypt {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitDecrypt)
	}
	// The message must stay generic: naming the failure would be an oracle.
	if !strings.Contains(err.Error(), "cannot decrypt") {
		t.Fatalf("error %q is not the generic decrypt message", err)
	}
	if entries, rerr := os.ReadDir(dir); rerr != nil {
		t.Fatalf("ReadDir: %v", rerr)
	} else if len(entries) != 0 {
		t.Fatalf("a failed reveal wrote %d entries", len(entries))
	}
}

func TestRevealPlainImageHasNoData(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(256, 256))
	dir := t.TempDir()

	_, _, err := runRevealCLI(t, carrierPath, "-o", dir, "--passphrase-file", passFile(t, "correct horse battery staple"))
	if codeOf(err) != ExitDecrypt {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitDecrypt)
	}
	if !strings.Contains(err.Error(), "cannot decrypt") {
		t.Fatalf("error %q is not the generic decrypt message", err)
	}
	if entries, rerr := os.ReadDir(dir); rerr != nil {
		t.Fatalf("ReadDir: %v", rerr)
	} else if len(entries) != 0 {
		t.Fatalf("revealing a plain image wrote %d entries", len(entries))
	}
}

func TestRevealRefusesOverwrite(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(256, 256))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("fresh content"))
	pass := "correct horse battery staple"
	passPath := passFile(t, pass)

	stegoPath := filepath.Join(t.TempDir(), "stego.png")
	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", stegoPath,
		"--passphrase-file", passPath); err != nil {
		t.Fatalf("hide: %v (stderr %s)", err, stderr)
	}

	dir := t.TempDir()
	existing := filepath.Join(dir, "note.txt")
	writeBytes(t, existing, []byte("old content"))

	_, _, err := runRevealCLI(t, stegoPath, "-o", dir, "--passphrase-file", passPath)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("reveal = %v, want an overwrite refusal", err)
	}
	assertFileEquals(t, existing, []byte("old content"))

	if _, stderr, err := runRevealCLI(t, stegoPath, "-o", dir, "--force",
		"--passphrase-file", passPath); err != nil {
		t.Fatalf("reveal --force: %v (stderr %s)", err, stderr)
	}
	assertFileEquals(t, existing, []byte("fresh content"))
}

func TestRevealMaxOutput(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(256, 256))
	content := make([]byte, 4096)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("rand: %v", err)
	}
	input := filepath.Join(t.TempDir(), "note.bin")
	writeBytes(t, input, content)
	pass := "correct horse battery staple"
	passPath := passFile(t, pass)

	stegoPath := filepath.Join(t.TempDir(), "stego.png")
	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", stegoPath,
		"--force", "--passphrase-file", passPath); err != nil {
		t.Fatalf("hide: %v (stderr %s)", err, stderr)
	}

	dir := t.TempDir()
	_, _, err := runRevealCLI(t, stegoPath, "-o", dir, "--max-output", "1024",
		"--passphrase-file", passPath)
	if codeOf(err) != ExitError {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitError)
	}
	if !strings.Contains(err.Error(), "max-output") {
		t.Fatalf("error %q does not point at --max-output", err)
	}

	if _, stderr, err := runRevealCLI(t, stegoPath, "-o", dir, "--passphrase-file", passPath); err != nil {
		t.Fatalf("reveal with the default limit: %v (stderr %s)", err, stderr)
	}
	assertFileEquals(t, filepath.Join(dir, "note.bin"), content)
}

// TestRevealRejectsUnsafeArchive proves the reader refuses a blob whose tar tries
// to write outside the destination, even though it decrypts cleanly.
func TestRevealRejectsUnsafeArchive(t *testing.T) {
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))

	parent := t.TempDir()
	dir := filepath.Join(parent, "out")
	stegoPath := filepath.Join(t.TempDir(), "stego.png")
	embedBlobAt(t, carrierPath, stegoPath, pass, 0, tarOf(t, "../escape.txt", []byte("pwned")))

	_, _, err := runRevealCLI(t, stegoPath, "-o", dir, "--passphrase-file", passFile(t, pass))
	if codeOf(err) != ExitError {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitError)
	}
	if !strings.Contains(err.Error(), "outside the output directory") {
		t.Fatalf("error %q does not explain the refusal", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the escape attempt created a file outside the output directory")
	}
}

// TestRevealCatchesCorruption flips one channel of a valid stego image and checks
// that the reader reports the documented exit code instead of returning garbage.
func TestRevealCatchesCorruption(t *testing.T) {
	pass := "correct horse battery staple"
	carrierPath := saveImage(t, noiseImage(256, 256))
	good := filepath.Join(t.TempDir(), "good.png")
	body := []byte("the payload survives a round trip")
	embedBlobAt(t, carrierPath, good, pass, 0, tarOf(t, "note.txt", body))

	dir := t.TempDir()
	if _, stderr, err := runRevealCLI(t, good, "-o", dir, "--passphrase-file", passFile(t, pass)); err != nil {
		t.Fatalf("reveal of a clean image: %v (stderr %s)", err, stderr)
	}
	assertFileEquals(t, filepath.Join(dir, "note.txt"), body)

	// Flip a salt channel: it carries a bit by construction, so the key changes
	// and the header can no longer be authenticated.
	loaded, err := carrier.Load(good)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	channels := stego.ChannelCount(loaded.Image)
	saltChannel := stego.SaltPositions(channels)[0]
	width := loaded.Image.Rect.Dx()
	pixel := int(saltChannel / 3)
	offset := loaded.Image.PixOffset(pixel%width, pixel/width)
	loaded.Image.Pix[offset+int(saltChannel%3)] ^= 1

	bad := filepath.Join(t.TempDir(), "bad.png")
	if err := carrier.Save(bad, loaded.Image, loaded.Chunks); err != nil {
		t.Fatalf("save corrupted: %v", err)
	}

	badDir := t.TempDir()
	_, _, err = runRevealCLI(t, bad, "-o", badDir, "--passphrase-file", passFile(t, pass))
	if codeOf(err) != ExitDecrypt {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitDecrypt)
	}
	if !strings.Contains(err.Error(), "cannot decrypt") {
		t.Fatalf("error %q is not the generic decrypt message", err)
	}
}
