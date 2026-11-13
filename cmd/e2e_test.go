package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests run the built binary as a subprocess rather than calling the
// command tree in-process. That is the only way to cover two things the engine
// tests cannot see: the process exit code for each documented failure, and
// --passphrase-stdin, which reads the real os.Stdin. The PRD's end-to-end
// acceptance list is what drives the scenarios below.

// binPath is the binary TestMain builds, shared by every E2E test.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wisanggeni-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: temp dir: %v\n", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "wisanggeni")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	build := exec.Command("go", "build", "-o", binPath, "github.com/a-axa/wisanggeni")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build failed: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// repoRoot walks up from the test's working directory to the module root, which
// is where `go build` must run from.
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// result is one finished subprocess.
type result struct {
	stdout string
	stderr string
	code   int
}

// runBinary executes the built binary with args and optional stdin.
func runBinary(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()

	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return result{stdout: out.String(), stderr: errOut.String(), code: code}
}

const e2ePass = "correct horse battery staple"

// TestE2ERoundTrip is the happy path end to end: hide with a passphrase on
// stdin, then reveal and read the payload back, with no in-process shortcut
// anywhere.
func TestE2ERoundTrip(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(256, 256))
	payload := filepath.Join(dir, "secret.txt")
	body := []byte(strings.Repeat("wisanggeni ", 40))
	writeBytes(t, payload, body)
	stegoPath := filepath.Join(dir, "out.png")

	got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", stegoPath, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("hide exit %d, want 0\nstdout: %s\nstderr: %s", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "wrote ") {
		t.Fatalf("hide stdout = %q, want a wrote line", got.stdout)
	}

	// The carrier itself must be untouched, and the output must differ from it.
	if bytes.Equal(readImageBytes(t, stegoPath), readImageBytes(t, carrierPath)) {
		t.Fatal("output is byte-identical to the carrier")
	}

	revealDir := filepath.Join(dir, "revealed")
	got = runBinary(t, e2ePass+"\n", "reveal", stegoPath, "-o", revealDir, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("reveal exit %d, want 0\nstdout: %s\nstderr: %s", got.code, got.stdout, got.stderr)
	}

	// Pack names entries after the input's basename, so the file lands under a
	// directory named after the temporary input directory.
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

// TestE2EWrongPassphrase checks the decrypt failure and its exit code.
func TestE2EWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(256, 256))
	payload := filepath.Join(dir, "secret.txt")
	writeBytes(t, payload, []byte("hello"))
	stegoPath := filepath.Join(dir, "out.png")

	if got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", stegoPath, "--passphrase-stdin"); got.code != ExitOK {
		t.Fatalf("hide exit %d: %s", got.code, got.stderr)
	}

	got := runBinary(t, "wrong passphrase entirely\n", "reveal", stegoPath,
		"-o", filepath.Join(dir, "out"), "--passphrase-stdin")
	if got.code != ExitDecrypt {
		t.Fatalf("reveal exit %d, want %d (decrypt)\nstderr: %s", got.code, ExitDecrypt, got.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); err == nil {
		t.Fatal("reveal wrote output despite a wrong passphrase")
	}
}

// TestE2ECapacityFailsBeforePrompt is the documented ordering guarantee: a
// payload that cannot fit fails without reading the passphrase at all. The fill
// gate fires first for an oversized payload, so --force suppresses it and leaves
// the capacity check as the thing that fails. Stdin is empty: were the ordering
// wrong, the prompt would consume it and fail differently.
func TestE2ECapacityFailsBeforePrompt(t *testing.T) {
	dir := t.TempDir()
	// A textured 128x128 carrier passes the cover check, so only the payload size
	// can stop the job.
	carrierPath := saveImage(t, noiseImage(128, 128))
	out := filepath.Join(dir, "out.png")

	// Incompressible bytes, far past the carrier's capacity.
	payload := filepath.Join(dir, "big.bin")
	body := make([]byte, 1<<16)
	rng := uint32(0xdeadbeef)
	for i := range body {
		rng = rng*1664525 + 1013904223
		body[i] = byte(rng >> 24)
	}
	writeBytes(t, payload, body)

	got := runBinary(t, "", "hide", payload,
		"-c", carrierPath, "-o", out, "--passphrase-stdin", "--force")
	if got.code != ExitCapacity {
		t.Fatalf("hide exit %d, want %d (capacity)\nstdout: %s\nstderr: %s",
			got.code, ExitCapacity, got.stdout, got.stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("hide wrote an output for an impossible payload")
	}
}

// TestE2ESmoothCoverIsRefused pins the cover-score gate and its exit code.
func TestE2ESmoothCoverIsRefused(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, flatImage(256, 256))
	payload := filepath.Join(dir, "secret.txt")
	writeBytes(t, payload, []byte("hello"))

	got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", filepath.Join(dir, "out.png"), "--passphrase-stdin")
	if got.code != ExitUnfit {
		t.Fatalf("hide exit %d, want %d (unfit)\nstderr: %s", got.code, ExitUnfit, got.stderr)
	}

	// --force overrides the refusal, which is the documented escape hatch.
	got = runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", filepath.Join(dir, "forced.png"), "--passphrase-stdin", "--force")
	if got.code != ExitOK {
		t.Fatalf("hide --force exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
}

// TestE2EOverwriteNeedsForce covers the output-exists rule.
func TestE2EOverwriteNeedsForce(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(256, 256))
	payload := filepath.Join(dir, "secret.txt")
	writeBytes(t, payload, []byte("hello"))
	out := filepath.Join(dir, "out.png")
	writeBytes(t, out, []byte("already here"))

	got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", out, "--passphrase-stdin")
	if got.code != ExitError {
		t.Fatalf("hide exit %d, want %d\nstderr: %s", got.code, ExitError, got.stderr)
	}
	if got := readImageBytes(t, out); string(got) != "already here" {
		t.Fatal("hide overwrote the existing output without --force")
	}

	got = runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", out, "--passphrase-stdin", "--force")
	if got.code != ExitOK {
		t.Fatalf("hide --force exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
}

// TestE2EOutputEqualsCarrier covers the three ways -o can name -c: the same
// literal path, a symlink to it, and a hardlink to it. All three must be
// refused, since the carrier is never modified.
func TestE2EOutputEqualsCarrier(t *testing.T) {
	t.Run("same path", func(t *testing.T) {
		dir := t.TempDir()
		carrierPath := saveImage(t, noiseImage(256, 256))
		payload := filepath.Join(dir, "secret.txt")
		writeBytes(t, payload, []byte("hello"))

		got := runBinary(t, e2ePass+"\n", "hide", payload,
			"-c", carrierPath, "-o", carrierPath, "--passphrase-stdin")
		if got.code == ExitOK {
			t.Fatalf("hide accepted -o == -c\nstdout: %s", got.stdout)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		carrierPath := saveImage(t, noiseImage(256, 256))
		link := filepath.Join(dir, "link.png")
		if err := os.Symlink(carrierPath, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		payload := filepath.Join(dir, "secret.txt")
		writeBytes(t, payload, []byte("hello"))

		got := runBinary(t, e2ePass+"\n", "hide", payload,
			"-c", carrierPath, "-o", link, "--passphrase-stdin")
		if got.code == ExitOK {
			t.Fatal("hide accepted an -o that is a symlink to the carrier")
		}
	})

	t.Run("hardlink", func(t *testing.T) {
		dir := t.TempDir()
		carrierPath := saveImage(t, noiseImage(256, 256))
		link := filepath.Join(dir, "hard.png")
		if err := os.Link(carrierPath, link); err != nil {
			t.Skipf("hardlink unsupported: %v", err)
		}
		payload := filepath.Join(dir, "secret.txt")
		writeBytes(t, payload, []byte("hello"))

		got := runBinary(t, e2ePass+"\n", "hide", payload,
			"-c", carrierPath, "-o", link, "--passphrase-stdin")
		if got.code == ExitOK {
			t.Fatal("hide accepted an -o that is a hardlink to the carrier")
		}
	})
}

// TestE2ENameCollision covers §5.1: two inputs whose root names collide are
// refused before anything is packed, because the archive could not be extracted
// unambiguously.
func TestE2ENameCollision(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(512, 512))

	// Two sibling directories both named "dup" cannot coexist, so the collision
	// is built from two trees whose leaf directories share a name.
	a := filepath.Join(dir, "a", "dup")
	b := filepath.Join(dir, "b", "dup")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", p, err)
		}
		writeBytes(t, filepath.Join(p, "f.txt"), []byte("content"))
	}

	got := runBinary(t, e2ePass+"\n", "hide", a, b,
		"-c", carrierPath, "-o", filepath.Join(dir, "out.png"), "--passphrase-stdin")
	if got.code == ExitOK {
		t.Fatalf("hide accepted two inputs with the same root name\nstdout: %s", got.stdout)
	}
	if !strings.Contains(got.stderr, "dup") {
		t.Fatalf("stderr %q does not name the collision", got.stderr)
	}
}

// TestE2EFillNeedsForce covers the fill-ratio gate. A 256x256 carrier admits
// about 12 KB, so a payload near 1.5 KB sits above the 10% force threshold while
// staying well inside the capacity, which is the band the gate exists for.
func TestE2EFillNeedsForce(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(256, 256))
	out := filepath.Join(dir, "out.png")

	// Incompressible bytes keep their size. T is 196608 for a 256x256 carrier and
	// fill is 8*blob/T, so 2600 bytes of body lands near 13%: past the 10% force
	// threshold but still inside the 12168-byte capacity.
	body := make([]byte, 2600)
	rng := uint32(0x12345678)
	for i := range body {
		rng = rng*1664525 + 1013904223
		body[i] = byte(rng >> 24)
	}
	payload := filepath.Join(dir, "fill.bin")
	writeBytes(t, payload, body)

	got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", out, "--passphrase-stdin")
	if got.code != ExitUnfit {
		t.Fatalf("hide exit %d, want %d (unfit)\nstderr: %s", got.code, ExitUnfit, got.stderr)
	}
	if !strings.Contains(got.stderr, "force") {
		t.Fatalf("stderr %q does not mention --force", got.stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("hide wrote output despite a fill refusal")
	}

	// --force accepts the same job.
	got = runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", out, "--passphrase-stdin", "--force")
	if got.code != ExitOK {
		t.Fatalf("hide --force exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
}

// TestE2EMultiFileRoundTrip hides several inputs at once and checks every one
// comes back, which is the case where entry naming and nesting have to line up.
func TestE2EMultiFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(512, 512))

	one := filepath.Join(dir, "one.txt")
	writeBytes(t, one, []byte("first file"))
	two := filepath.Join(dir, "two", "two.txt")
	if err := os.MkdirAll(filepath.Dir(two), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeBytes(t, two, []byte("second file"))

	stegoPath := filepath.Join(dir, "out.png")
	got := runBinary(t, e2ePass+"\n", "hide", one, two,
		"-c", carrierPath, "-o", stegoPath, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("hide exit %d\nstderr: %s", got.code, got.stderr)
	}

	revealDir := filepath.Join(dir, "revealed")
	got = runBinary(t, e2ePass+"\n", "reveal", stegoPath, "-o", revealDir, "--passphrase-stdin")
	if got.code != ExitOK {
		t.Fatalf("reveal exit %d\nstderr: %s", got.code, got.stderr)
	}
	if !walkHasFile(revealDir, "two.txt") {
		t.Fatalf("nested input two.txt not revealed under %s", revealDir)
	}
	if !walkHasFile(revealDir, "one.txt") {
		t.Fatalf("input one.txt not revealed under %s", revealDir)
	}
}

// TestE2EInspectWithoutPromptAndAlphaCarrier covers two more PRD items: inspect
// names the payload with no passphrase, and an alpha carrier is refused.
func TestE2EInspectAndAlpha(t *testing.T) {
	dir := t.TempDir()
	carrierPath := saveImage(t, noiseImage(256, 256))
	payload := filepath.Join(dir, "secret.txt")
	writeBytes(t, payload, []byte(strings.Repeat("data ", 50)))
	stegoPath := filepath.Join(dir, "out.png")

	if got := runBinary(t, e2ePass+"\n", "hide", payload,
		"-c", carrierPath, "-o", stegoPath, "--passphrase-stdin"); got.code != ExitOK {
		t.Fatalf("hide exit %d: %s", got.code, got.stderr)
	}

	// With a closed stdin and no passphrase source, inspect must not block.
	got := runBinary(t, "", "inspect", stegoPath)
	if got.code != ExitOK {
		t.Fatalf("inspect exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}

	// An alpha carrier is reported as unusable rather than opaque: capacity and
	// score still describe the channels, and the report says plainly that
	// nothing can be hidden with it.
	alphaPath := writeAlphaPNG(t, 64, 64)
	got = runBinary(t, "", "capacity", alphaPath)
	if got.code != ExitOK {
		t.Fatalf("capacity on an alpha carrier exit %d, want 0\nstderr: %s", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "alpha") {
		t.Fatalf("capacity output does not mention alpha:\n%s", got.stdout)
	}

	// hide, however, must refuse to write anything with it.
	payloadPath := filepath.Join(dir, "p.txt")
	writeBytes(t, payloadPath, []byte("hello"))
	got = runBinary(t, e2ePass+"\n", "hide", payloadPath,
		"-c", alphaPath, "-o", filepath.Join(dir, "alpha-out.png"), "--passphrase-stdin")
	if got.code == ExitOK {
		t.Fatalf("hide accepted an alpha carrier\nstdout: %s", got.stdout)
	}
}

func walkHasFile(root, name string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = true
		}
		return nil
	})
	return found
}

func readImageBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}
