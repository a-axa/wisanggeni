package hide

import (
	"bytes"
	"crypto/rand"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/reveal"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// bmpImage writes img as a BMP carrier and returns its path.
func bmpImage(t *testing.T, img *image.NRGBA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "carrier.bmp")
	if err := carrier.Save(path, img, nil); err != nil {
		t.Fatalf("save BMP carrier: %v", err)
	}
	return path
}

// bmpJob builds a hide job whose carrier and output are both BMP.
func bmpJob(t *testing.T, side int, payload []byte) Options {
	t.Helper()
	input := filepath.Join(t.TempDir(), "payload.bin")
	writeBytes(t, input, payload)
	return Options{
		Inputs:      []string{input},
		CarrierPath: bmpImage(t, noiseImage(side, side)),
		OutputPath:  filepath.Join(t.TempDir(), "out.bmp"),
		Passphrase:  []byte("correct horse battery staple"),
		Force:       true, // the 50% case sits above the default gate
	}
}

// bmpRoundTrip hides payload in a BMP carrier, reveals it from the BMP output,
// and returns the bytes that came back.
func bmpRoundTrip(t *testing.T, job Options) []byte {
	t.Helper()

	if _, err := Run(job); err != nil {
		t.Fatalf("hide: %v", err)
	}

	// The output must really be a BMP, not a PNG with a .bmp name.
	raw, err := os.ReadFile(job.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(raw) < 2 || raw[0] != 'B' || raw[1] != 'M' {
		t.Fatalf("output is not a BMP: % x", raw[:min(4, len(raw))])
	}

	written, err := carrier.Load(job.OutputPath)
	if err != nil {
		t.Fatalf("load output: %v", err)
	}
	dir := t.TempDir()
	if _, err := reveal.Open(written.Image, reveal.Options{
		Dir:        dir,
		Passphrase: job.Passphrase,
	}); err != nil {
		t.Fatalf("reveal: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "payload.bin"))
	if err != nil {
		t.Fatalf("read revealed file: %v", err)
	}
	return got
}

// TestBMPRoundTripSmall covers the 1 KB case.
func TestBMPRoundTripSmall(t *testing.T) {
	payload := bytes.Repeat([]byte("wisanggeni "), 102)[:1024]

	got := bmpRoundTrip(t, bmpJob(t, 256, payload))
	if !bytes.Equal(got, payload) {
		t.Fatalf("revealed %d bytes, want %d; content differs", len(got), len(payload))
	}
}

// TestBMPRoundTripOneMegabyte covers the 1 MB case. The payload repeats, so zstd
// brings it under the carrier's capacity; an incompressible megabyte would need
// a carrier large enough to break the 50% fill rule.
func TestBMPRoundTripOneMegabyte(t *testing.T) {
	const line = "wisanggeni bmp carrier round trip line\n"
	payload := bytes.Repeat([]byte(line), 1<<20/len(line)+1)[:1<<20]

	job := bmpJob(t, 512, payload)
	if _, err := Analyze(job); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	got := bmpRoundTrip(t, job)
	if !bytes.Equal(got, payload) {
		t.Fatalf("revealed %d bytes, want %d; content differs", len(got), len(payload))
	}
}

// TestBMPRoundTripHalfCapacity covers the ~50% fill case, where the STC encoder
// is working hardest.
func TestBMPRoundTripHalfCapacity(t *testing.T) {
	const side = 256
	// Fill is measured on the blob and one blob byte costs eight channels, so a
	// half fill is channels/16, which is almost exactly the full capacity. Pack
	// refuses a payload above capacity before the fill policy runs, so the body is
	// set just under the ceiling; inert random bytes keep zstd from shrinking it.
	payload := make([]byte, stego.Capacity(side, side)-format.BlobOverhead)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}

	job := bmpJob(t, side, payload)
	plan, err := Analyze(job)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if plan.Fill < 0.40 || plan.Fill > 0.60 {
		t.Fatalf("fill = %.3f, want roughly 0.5; the test would not exercise the intended load", plan.Fill)
	}

	got := bmpRoundTrip(t, job)
	if !bytes.Equal(got, payload) {
		t.Fatalf("revealed %d bytes, want %d; content differs", len(got), len(payload))
	}
}

// TestBMPCapacityMatchesPNG checks that the carrier format does not change the
// engine's budget: capacity is a function of dimensions, not of the extension.
func TestBMPCapacityMatchesPNG(t *testing.T) {
	payload := bytes.Repeat([]byte("capacity parity "), 64)
	bmp := bmpJob(t, 192, payload)
	png := bmp
	png.OutputPath = filepath.Join(t.TempDir(), "out.png")

	bmpPlan, err := Analyze(bmp)
	if err != nil {
		t.Fatalf("analyze BMP: %v", err)
	}
	pngPlan, err := Analyze(png)
	if err != nil {
		t.Fatalf("analyze PNG: %v", err)
	}
	if bmpPlan.Capacity != pngPlan.Capacity {
		t.Errorf("capacity = %d for BMP, %d for PNG; format must not change it",
			bmpPlan.Capacity, pngPlan.Capacity)
	}
	// A BMP carrier has no ancillary chunks, so the report must say so rather
	// than inherit a stale list.
	if len(bmpPlan.Chunks) != 0 {
		t.Errorf("Chunks = %v for a BMP carrier, want none", bmpPlan.Chunks)
	}
}

// TestBMPVerifyOutputCatchesCorruption is the auto-verify case for BMP: embed a
// real blob into a BMP, flip a salt channel, and check the documented exit code.
func TestBMPVerifyOutputCatchesCorruption(t *testing.T) {
	carrierPath := bmpImage(t, noiseImage(96, 96))
	input := filepath.Join(t.TempDir(), "note.txt")
	writeBytes(t, input, []byte("hello wisanggeni"))

	packed, err := archive.Pack([]string{input}, archive.Options{MaxBytes: stego.Capacity(96, 96)})
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
	good := filepath.Join(t.TempDir(), "good.bmp")
	if err := carrier.Save(good, car.Image, car.Chunks); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := verifyOutput(good, blob, keys.PosSeed); err != nil {
		t.Fatalf("a clean BMP failed verification: %v", err)
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
	bad := filepath.Join(t.TempDir(), "bad.bmp")
	if err := carrier.Save(bad, corrupted.Image, corrupted.Chunks); err != nil {
		t.Fatalf("save corrupted: %v", err)
	}

	err = verifyOutput(bad, blob, keys.PosSeed)
	if err == nil {
		t.Fatal("verification accepted a corrupted BMP")
	}
	if code := exitcode.Code(err); code != exitcode.Verify {
		t.Fatalf("exit = %d, want %d", code, exitcode.Verify)
	}
}
