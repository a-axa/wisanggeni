package hide

import (
	"bytes"
	"crypto/rand"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
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

// fixedSalt is the determinism tests' pinned salt.
var fixedSalt = bytes.Repeat([]byte{0x42}, format.SaltSize)

// fixedNonces is the determinism tests' pinned nonce pair.
func fixedNonces() *Nonces {
	return &Nonces{
		Header: bytes.Repeat([]byte{0x11}, format.NonceSize),
		Body:   bytes.Repeat([]byte{0x22}, format.NonceSize),
	}
}

// sampleJob builds the same small job every test starts from.
func sampleJob(t *testing.T) Options {
	t.Helper()
	carrierPath := saveImage(t, noiseImage(256, 256))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, bytes.Repeat([]byte("wisanggeni "), 20))
	return Options{
		Inputs:      []string{input},
		CarrierPath: carrierPath,
		OutputPath:  filepath.Join(t.TempDir(), "out.png"),
		Passphrase:  []byte("correct horse battery staple"),
	}
}

func TestClassifyFill(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fill  float64
		force bool
		want  fillDecision
	}{
		{0.01, false, fillOK},
		{0.05, false, fillOK},
		{0.06, false, fillWarn},
		{0.10, false, fillWarn},
		{0.11, false, fillNeedForce},
		{0.11, true, fillWarn},
		{0.50, true, fillWarn},
		{0.51, true, fillReject},
		{0.99, false, fillReject},
	}
	for _, tc := range cases {
		if got := classifyFill(tc.fill, tc.force); got != tc.want {
			t.Fatalf("classifyFill(%.2f, %v) = %d, want %d", tc.fill, tc.force, got, tc.want)
		}
	}
}

// TestAnalyzeRefusesCapacityBeforePassphrase pins the §7.8 ordering: an 8x8
// carrier has no usable capacity, so Analyze must fail with exit 2 without ever
// needing a passphrase.
func TestAnalyzeRefusesCapacityBeforePassphrase(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(8, 8))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))

	_, err := Analyze(Options{
		Inputs:      []string{input},
		CarrierPath: carrierPath,
		OutputPath:  filepath.Join(t.TempDir(), "out.png"),
		Force:       true,
	})
	if code := exitcode.Code(err); code != exitcode.Capacity {
		t.Fatalf("exit = %d (%v), want %d", code, err, exitcode.Capacity)
	}
	if !strings.Contains(err.Error(), "capacity is 0 bytes") {
		t.Fatalf("error %q does not report the capacity", err)
	}
}

func TestAnalyzeRefusesSmoothCarrierWithoutForce(t *testing.T) {
	carrierPath := saveImage(t, flatImage(256, 256))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello"))

	opts := Options{
		Inputs:      []string{input},
		CarrierPath: carrierPath,
		OutputPath:  filepath.Join(t.TempDir(), "out.png"),
	}
	_, err := Analyze(opts)
	if code := exitcode.Code(err); code != exitcode.Unfit {
		t.Fatalf("exit = %d (%v), want %d", code, err, exitcode.Unfit)
	}
	if !strings.Contains(err.Error(), "smooth") {
		t.Fatalf("error %q does not explain the refusal", err)
	}

	opts.Force = true
	if _, err := Analyze(opts); err != nil {
		t.Fatalf("Analyze with force: %v", err)
	}
}

// TestAnalyzeWarningsReachBothSinks checks that the plan keeps the advisory
// messages and that the caller's sink sees the same text, which is what lets the
// CLI print live and the TUI render them from the message it gets back.
func TestAnalyzeWarningsReachBothSinks(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(128, 128))
	input := filepath.Join(t.TempDir(), "blob.bin")
	random := make([]byte, 100)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeBytes(t, input, random)

	var seen []string
	plan, err := Analyze(Options{
		Inputs:      []string{input},
		CarrierPath: carrierPath,
		OutputPath:  filepath.Join(t.TempDir(), "out.png"),
		NoCompress:  true,
		Force:       true,
		Warn:        func(msg string) { seen = append(seen, msg) },
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(plan.Warnings) == 0 {
		t.Fatal("the plan recorded no warnings")
	}
	if len(seen) != len(plan.Warnings) {
		t.Fatalf("sink saw %d warnings, plan holds %d", len(seen), len(plan.Warnings))
	}
	for i := range seen {
		if seen[i] != plan.Warnings[i] {
			t.Fatalf("warning %d = %q, want %q", i, seen[i], plan.Warnings[i])
		}
	}
}

// TestPinnedRandomnessIsDeterministic is the engine half of the byte-identity
// guarantee: with the salt and both nonces pinned, two runs produce the same
// file. The TUI and the CLI both go through this code, so neither can add a
// byte of its own.
func TestPinnedRandomnessIsDeterministic(t *testing.T) {
	first := sampleJob(t)
	first.Salt = fixedSalt
	first.Nonces = fixedNonces()

	second := first
	second.OutputPath = filepath.Join(t.TempDir(), "out.png")

	if _, err := Run(first); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, err := Run(second); err != nil {
		t.Fatalf("second run: %v", err)
	}

	a, err := os.ReadFile(first.OutputPath)
	if err != nil {
		t.Fatalf("read first output: %v", err)
	}
	b, err := os.ReadFile(second.OutputPath)
	if err != nil {
		t.Fatalf("read second output: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("two pinned runs differ: %d bytes vs %d", len(a), len(b))
	}
}

// TestRunRoundTripsThroughReveal exercises the writer against the reader, which
// is the pair the TUI wires together.
func TestRunRoundTripsThroughReveal(t *testing.T) {
	job := sampleJob(t)
	content := []byte("the payload survives the round trip")
	writeBytes(t, job.Inputs[0], content)

	if _, err := Run(job); err != nil {
		t.Fatalf("hide: %v", err)
	}

	written, err := carrier.Load(job.OutputPath)
	if err != nil {
		t.Fatalf("load output: %v", err)
	}
	dir := t.TempDir()
	result, err := reveal.Open(written.Image, reveal.Options{
		Dir:        dir,
		Passphrase: job.Passphrase,
	})
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("revealed %d entries, want 1", len(result.Entries))
	}

	got, err := os.ReadFile(filepath.Join(dir, "note.txt"))
	if err != nil {
		t.Fatalf("read revealed file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("revealed content differs from the input")
	}
}

// TestVerifyOutputCatchesCorruption embeds a real blob, saves it, flips one
// channel, and checks that verification reports the documented exit code.
func TestVerifyOutputCatchesCorruption(t *testing.T) {
	carrierPath := saveImage(t, noiseImage(96, 96))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello wisanggeni"))

	capacity := stego.Capacity(96, 96)
	packed, err := archive.Pack([]string{input}, archive.Options{MaxBytes: capacity})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	salt := make([]byte, format.SaltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("rand: %v", err)
	}
	keys, err := crypto.Derive("correct horse battery staple", salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	blob, err := crypto.Seal(keys.EncKey, salt, format.FlagZstd, packed.Data)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	car, err := carrier.Load(carrierPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := stego.EmbedBlob(car.Image, blob, keys.PosSeed, stego.CostMap(car.Image)); err != nil {
		t.Fatalf("embed: %v", err)
	}
	good := filepath.Join(t.TempDir(), "good.png")
	if err := carrier.Save(good, car.Image, car.Chunks); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := verifyOutput(good, blob, keys.PosSeed); err != nil {
		t.Fatalf("a clean file failed verification: %v", err)
	}

	corrupted, err := carrier.Load(good)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Flip a channel the salt occupies: it is guaranteed to carry a bit, so the
	// comparison cannot be fooled by an unused channel.
	channels := stego.ChannelCount(corrupted.Image)
	saltChannel := stego.SaltPositions(channels)[0]
	pixel := int(saltChannel / 3)
	width := corrupted.Image.Rect.Dx()
	offset := corrupted.Image.PixOffset(pixel%width, pixel/width)
	corrupted.Image.Pix[offset+int(saltChannel%3)] ^= 1
	bad := filepath.Join(t.TempDir(), "bad.png")
	if err := carrier.Save(bad, corrupted.Image, corrupted.Chunks); err != nil {
		t.Fatalf("save corrupted: %v", err)
	}

	err = verifyOutput(bad, blob, keys.PosSeed)
	if err == nil {
		t.Fatal("verification accepted a corrupted file")
	}
	if code := exitcode.Code(err); code != exitcode.Verify {
		t.Fatalf("exit = %d, want %d", code, exitcode.Verify)
	}
}

// TestAnalyzeRejectsEmptyInput keeps the engine honest for a caller that forgot
// its own check: the TUI hands it whatever the wizard collected.
func TestAnalyzeRejectsEmptyInput(t *testing.T) {
	_, err := Analyze(Options{CarrierPath: "c.png", OutputPath: "o.png"})
	if err == nil {
		t.Fatal("Analyze accepted a job with no inputs")
	}
	if code := exitcode.Code(err); code != exitcode.Error {
		t.Fatalf("exit = %d, want %d", code, exitcode.Error)
	}
}
