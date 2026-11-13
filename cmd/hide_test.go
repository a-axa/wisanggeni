package cmd

import (
	"bytes"
	"crypto/rand"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/hide"
	"github.com/a-axa/wisanggeni/internal/reveal"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// noiseImage builds a carrier with high texture, which CoverScore rates as
// suitable.
func noiseImage(w, h int) *image.NRGBA {
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

// flatImage builds a perfectly smooth carrier, which CoverScore rejects.
func flatImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 128, 128, 128, 0xff
	}
	return img
}

func saveImage(t *testing.T, img *image.NRGBA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "carrier.png")
	if err := carrier.Save(path, img, nil); err != nil {
		t.Fatalf("save carrier: %v", err)
	}
	return path
}

func writeBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func passFile(t *testing.T, pass string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pass.txt")
	writeBytes(t, path, []byte(pass+"\n"))
	return path
}

// runHide drives the real command the way the binary would.
func runHideCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := newHideCmd()
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	c.SilenceErrors = true
	c.SilenceUsage = true
	err = c.Execute()
	return out.String(), errOut.String(), err
}

// codeOf maps an error the same way Execute does.
func codeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return ExitError
}

func TestHideRoundTrip(t *testing.T) {
	inputs := bytes.Repeat([]byte("wisanggeni "), 40)
	for _, tc := range []struct {
		name       string
		flags      []string
		noCompress bool
	}{
		{name: "zstd", flags: nil},
		{name: "no-compress", flags: []string{"--no-compress"}, noCompress: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			carrierPath := saveImage(t, noiseImage(256, 256))
			input := filepath.Join(t.TempDir(), "note.txt")
			writeBytes(t, input, inputs)
			out := filepath.Join(t.TempDir(), "out.png")
			pass := "correct horse battery staple"

			args := append([]string{input, "-c", carrierPath, "-o", out,
				"--passphrase-file", passFile(t, pass)}, tc.flags...)
			stdout, stderr, err := runHideCLI(t, args...)
			if err != nil {
				t.Fatalf("hide: %v (stderr: %s)", err, stderr)
			}
			if !strings.Contains(stdout, "wrote") {
				t.Fatalf("stdout = %q, want a write notice", stdout)
			}

			// The result must be a normal PNG of the same size.
			got, err := carrier.Load(out)
			if err != nil {
				t.Fatalf("output is not a readable PNG: %v", err)
			}
			if got.Image.Rect.Dx() != 256 || got.Image.Rect.Dy() != 256 {
				t.Fatalf("dimensions = %v, want 256x256", got.Image.Rect)
			}

			// Decrypt it back through the documented reader path.
			capacity := stego.Capacity(256, 256)
			packed, err := archive.Pack([]string{input}, archive.Options{
				MaxBytes:   capacity,
				NoCompress: tc.noCompress,
			})
			if err != nil {
				t.Fatalf("re-pack: %v", err)
			}
			if packed.Compressed == tc.noCompress {
				t.Fatalf("compressed = %v, want the opposite of noCompress", packed.Compressed)
			}

			salt, err := stego.ExtractSalt(got.Image, format.SaltSize)
			if err != nil {
				t.Fatalf("extract salt: %v", err)
			}
			keys, err := crypto.Derive(pass, salt)
			if err != nil {
				t.Fatalf("derive: %v", err)
			}
			blob, err := stego.ExtractBlob(got.Image,
				format.BlobOverhead+len(packed.Data), keys.PosSeed)
			if err != nil {
				t.Fatalf("extract blob: %v", err)
			}
			header, body, err := crypto.Open(keys.EncKey, blob, uint64(capacity))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if header.Version != format.Version {
				t.Fatalf("version = %d", header.Version)
			}
			wantFlags := uint8(0)
			if packed.Compressed {
				wantFlags = format.FlagZstd
			}
			if header.Flags != wantFlags {
				t.Fatalf("flags = %d, want %d", header.Flags, wantFlags)
			}
			if !bytes.Equal(body, packed.Data) {
				t.Fatalf("body differs from the packed payload")
			}
		})
	}
}

func TestHideCapacityFailsBeforePrompt(t *testing.T) {
	// An 8x8 carrier has zero usable capacity. The passphrase file does not
	// exist, so reaching the prompt would produce a different error: seeing exit
	// 2 proves the capacity check runs first.
	carrierPath := saveImage(t, noiseImage(8, 8))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))
	out := filepath.Join(t.TempDir(), "out.png")

	_, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", out,
		"--force", "--passphrase-file", filepath.Join(t.TempDir(), "missing"))
	if codeOf(err) != ExitCapacity {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitCapacity)
	}
	if !strings.Contains(stderr+err.Error(), "capacity is 0 bytes") {
		t.Fatalf("message %q does not report the capacity", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed hide left an output file")
	}
}

func TestHideOutputIsCarrier(t *testing.T) {
	base := saveImage(t, noiseImage(96, 96))
	before, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("read carrier: %v", err)
	}
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))
	pass := passFile(t, "correct horse battery staple")

	cases := map[string]string{"same path": base}

	// A differently spelled path to the same file: filepath.Join would clean the
	// "." away, so build it by hand.
	sep := string(filepath.Separator)
	cases["dotted path"] = filepath.Dir(base) + sep + "." + sep + filepath.Base(base)

	symlink := filepath.Join(t.TempDir(), "link.png")
	if err := os.Symlink(base, symlink); err == nil {
		cases["symlink"] = symlink
	}
	hardlink := filepath.Join(t.TempDir(), "hard.png")
	if err := os.Link(base, hardlink); err == nil {
		cases["hardlink"] = hardlink
	}

	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			for _, force := range []bool{false, true} {
				args := []string{input, "-c", base, "-o", out, "--passphrase-file", pass}
				if force {
					args = append(args, "--force")
				}
				_, _, err := runHideCLI(t, args...)
				if err == nil {
					t.Fatalf("force=%v: hide accepted the carrier as output", force)
				}
				if !strings.Contains(err.Error(), "is the carrier") {
					t.Fatalf("force=%v: error = %v, want a carrier refusal", force, err)
				}
			}
		})
	}

	after, err := os.ReadFile(base)
	if err != nil {
		t.Fatalf("read carrier: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the carrier was modified")
	}
}

func TestHideExistingOutputNeedsForce(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(96, 96))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))
	out := filepath.Join(t.TempDir(), "out.png")
	writeBytes(t, out, []byte("old content"))
	pass := passFile(t, "correct horse battery staple")

	_, _, err := runHideCLI(t, input, "-c", carrierPath, "-o", out, "--passphrase-file", pass)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("hide = %v, want an overwrite refusal", err)
	}
	if got, _ := os.ReadFile(out); string(got) != "old content" {
		t.Fatalf("existing output was modified: %q", got)
	}

	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", out,
		"--force", "--passphrase-file", pass); err != nil {
		t.Fatalf("hide --force: %v (stderr %s)", err, stderr)
	}
	if _, err := carrier.Load(out); err != nil {
		t.Fatalf("output after force is not a PNG: %v", err)
	}
}

func TestHideSmoothCarrierNeedsForce(t *testing.T) {
	carrierPath := saveImage(t, flatImage(256, 256))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))
	pass := passFile(t, "correct horse battery staple")
	out := filepath.Join(t.TempDir(), "out.png")

	_, _, err := runHideCLI(t, input, "-c", carrierPath, "-o", out, "--passphrase-file", pass)
	if codeOf(err) != ExitUnfit {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitUnfit)
	}
	if !strings.Contains(err.Error(), "smooth") {
		t.Fatalf("error %q does not explain the refusal", err)
	}

	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", out,
		"--force", "--passphrase-file", pass); err != nil {
		t.Fatalf("hide --force: %v (stderr %s)", err, stderr)
	}
}

func TestHideFillNeedsForce(t *testing.T) {
	// A 128x128 carrier gives about 2.9 KB of capacity; a small file packs to a
	// 2 KB tar, which is roughly a third of the image once uncompressed.
	carrierPath := saveImage(t, noiseImage(128, 128))
	input := filepath.Join(t.TempDir(), "blob.bin")
	random := make([]byte, 100)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeBytes(t, input, random)
	pass := passFile(t, "correct horse battery staple")
	out := filepath.Join(t.TempDir(), "out.png")

	_, _, err := runHideCLI(t, input, "-c", carrierPath, "-o", out,
		"--no-compress", "--passphrase-file", pass)
	if codeOf(err) != ExitUnfit {
		t.Fatalf("exit = %d (%v), want %d", codeOf(err), err, ExitUnfit)
	}
	if !strings.Contains(err.Error(), "fill") {
		t.Fatalf("error %q does not mention fill", err)
	}

	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", out,
		"--no-compress", "--force", "--passphrase-file", pass); err != nil {
		t.Fatalf("hide --force: %v (stderr %s)", err, stderr)
	}
}

func TestHideLeavesNoTemporaryFiles(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(96, 96))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))
	outDir := t.TempDir()
	out := filepath.Join(outDir, "out.png")
	pass := passFile(t, "correct horse battery staple")

	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", out,
		"--passphrase-file", pass); err != nil {
		t.Fatalf("hide: %v (stderr %s)", err, stderr)
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "out.png" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("output directory holds %v, want only out.png", names)
	}
}

// TestHideCLIMatchesEngine proves the CLI adds nothing to the pipeline. It runs
// a real hide, reads the salt and both AEAD nonces back out of the written image,
// and repeats the same job through internal/hide with every random input pinned.
// The bytes must be identical: the CLI is a thin wrapper over the engine, and the
// engine is what the TUI wizard calls, so this is what keeps the two surfaces
// byte-identical.
func TestHideCLIMatchesEngine(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(256, 256))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, bytes.Repeat([]byte("wisanggeni "), 20))
	pass := "correct horse battery staple"

	cliOut := filepath.Join(t.TempDir(), "cli.png")
	if _, stderr, err := runHideCLI(t, input, "-c", carrierPath, "-o", cliOut,
		"--passphrase-file", passFile(t, pass)); err != nil {
		t.Fatalf("hide: %v (stderr %s)", err, stderr)
	}
	cliBytes, err := os.ReadFile(cliOut)
	if err != nil {
		t.Fatalf("read CLI output: %v", err)
	}

	// Every random value the writer used is recoverable from its own output: the
	// salt sits at public positions, and the nonces are the first bytes of the
	// payload the salt keys.
	written, err := carrier.Load(cliOut)
	if err != nil {
		t.Fatalf("load CLI output: %v", err)
	}
	salt, err := stego.ExtractSalt(written.Image, format.SaltSize)
	if err != nil {
		t.Fatalf("extract salt: %v", err)
	}
	keys, err := crypto.Derive(pass, salt)
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

	engineOut := filepath.Join(t.TempDir(), "engine.png")
	result, err := hide.Run(hide.Options{
		Inputs:      []string{input},
		CarrierPath: carrierPath,
		OutputPath:  engineOut,
		Passphrase:  []byte(pass),
		Salt:        salt,
		Nonces:      nonces,
	})
	if err != nil {
		t.Fatalf("engine run: %v", err)
	}
	if !result.Verified {
		t.Fatal("engine run did not verify its output")
	}

	engineBytes, err := os.ReadFile(engineOut)
	if err != nil {
		t.Fatalf("read engine output: %v", err)
	}
	if !bytes.Equal(cliBytes, engineBytes) {
		t.Fatalf("CLI and engine outputs differ: %d bytes vs %d", len(cliBytes), len(engineBytes))
	}
}
