package carrier_test

import (
	"bytes"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/bmp"

	"github.com/a-axa/wisanggeni/internal/carrier"
)

func TestLoadBMPNormalizesPixels(t *testing.T) {
	img := gradient(16, 12)
	path := writeBMPFile(t, encodeBMP(t, img))

	// A 24-bit BMP decodes as *image.RGBA, not NRGBA. The normalisation step is
	// what keeps the engine independent of the decoder's choice.
	decoded, kind, err := image.Decode(bytes.NewReader(readFile(t, path)))
	if err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if kind != "bmp" {
		t.Fatalf("decoded kind = %q, want bmp", kind)
	}
	if _, ok := decoded.(*image.RGBA); !ok {
		t.Fatalf("24-bit BMP decoded as %T, expected *image.RGBA", decoded)
	}

	c, err := carrier.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := c.Image.Rect, image.Rect(0, 0, 16, 12); got != want {
		t.Fatalf("Rect = %v, want %v", got, want)
	}
	if !bytes.Equal(c.Image.Pix, img.Pix) {
		t.Fatal("normalised pixels differ from the source image")
	}
	// BMP has no ancillary chunks, so nothing can be carried over.
	if len(c.Chunks) != 0 {
		t.Errorf("Chunks = %d, want 0", len(c.Chunks))
	}
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", c.Warnings)
	}
	if c.Alpha {
		t.Error("Alpha = true for an opaque BMP")
	}
}

func TestLoadBMPIsRowOrderIndependent(t *testing.T) {
	// BMP stores rows bottom-up; the decoder is expected to flip them. A
	// gradient is asymmetric in y, so a missed flip would reverse it visibly.
	img := gradient(7, 9)
	path := writeBMPFile(t, encodeBMP(t, img))

	c, err := carrier.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(c.Image.Pix, img.Pix) {
		t.Fatal("pixels came back flipped or reordered")
	}
}

func TestSaveBMPRoundTrip(t *testing.T) {
	src := gradient(9, 7)
	out := filepath.Join(t.TempDir(), "out.bmp")
	if err := carrier.Save(out, src, nil); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The output must actually be a BMP, not a PNG with a .bmp name.
	raw := readFile(t, out)
	if len(raw) < 2 || raw[0] != 'B' || raw[1] != 'M' {
		header := raw
		if len(header) > 4 {
			header = header[:4]
		}
		t.Fatalf("output does not start with the BM signature: % x", header)
	}

	typed, kind, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if kind != "bmp" {
		t.Fatalf("output kind = %q, want bmp", kind)
	}
	// 24 bpp, no alpha byte: the writer picks that for an opaque NRGBA.
	if _, ok := typed.(*image.RGBA); !ok {
		t.Fatalf("output decoded as %T, want *image.RGBA (24-bit)", typed)
	}

	reloaded, err := carrier.Load(out)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !bytes.Equal(reloaded.Image.Pix, src.Pix) {
		t.Fatal("pixels changed across Save and Load")
	}
}

func TestSaveBMPIsLosslessOverManyValues(t *testing.T) {
	// Every channel value, so a palette or bit-depth shortcut would show up as
	// a mismatch rather than passing on a lucky gradient.
	img := image.NewNRGBA(image.Rect(0, 0, 256, 4))
	for x := 0; x < 256; x++ {
		for y := 0; y < 4; y++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(x)
			img.Pix[i+1] = uint8(255 - x)
			img.Pix[i+2] = uint8((x * 7) % 256)
			img.Pix[i+3] = 0xff
		}
	}

	out := filepath.Join(t.TempDir(), "ramp.bmp")
	if err := carrier.Save(out, img, nil); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := carrier.Load(out)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(reloaded.Image.Pix, img.Pix) {
		t.Fatal("channel values did not survive the BMP round trip")
	}
}

func TestSaveBMPIgnoresChunks(t *testing.T) {
	// Chunks are a PNG concept. Passing them for a BMP output must not fail and
	// must not embed anything.
	out := filepath.Join(t.TempDir(), "out.bmp")
	err := carrier.Save(out, gradient(4, 4), []carrier.Chunk{
		{Type: "gAMA", Data: []byte{0, 0, 0, 1}},
		{Type: "tEXt", Data: []byte("Comment\x00secret")},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw := readFile(t, out)
	if bytes.Contains(raw, []byte("gAMA")) || bytes.Contains(raw, []byte("secret")) {
		t.Fatal("BMP output contains chunk data")
	}

	c, err := carrier.Load(out)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Chunks) != 0 {
		t.Errorf("Chunks = %v, want none for a BMP", chunkTypesOf(c.Chunks))
	}
}

func TestSaveFormatFollowsExtension(t *testing.T) {
	dir := t.TempDir()
	img := gradient(5, 5)

	cases := []struct {
		name string
		want string
	}{
		{"a.bmp", "bmp"},
		{"b.BMP", "bmp"},
		{"c.png", "png"},
		{"d", "png"}, // no extension: PNG remains the default
	}
	for _, tc := range cases {
		out := filepath.Join(dir, tc.name)
		if err := carrier.Save(out, img, nil); err != nil {
			t.Fatalf("Save(%s): %v", tc.name, err)
		}
		_, kind, err := image.Decode(bytes.NewReader(readFile(t, out)))
		if err != nil {
			t.Fatalf("decode %s: %v", tc.name, err)
		}
		if kind != tc.want {
			t.Errorf("%s decoded as %q, want %q", tc.name, kind, tc.want)
		}
	}
}

func TestLoadSniffsContentNotExtension(t *testing.T) {
	dir := t.TempDir()

	// PNG bytes under a .bmp name.
	pngPath := filepath.Join(dir, "actually.png.bmp")
	if err := os.WriteFile(pngPath, encodePNG(t, gradient(6, 6)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c, err := carrier.Load(pngPath)
	if err != nil {
		t.Fatalf("Load PNG named .bmp: %v", err)
	}
	if !bytes.Equal(c.Image.Pix, gradient(6, 6).Pix) {
		t.Fatal("PNG named .bmp did not load correctly")
	}

	// BMP bytes under a .png name.
	bmpPath := filepath.Join(dir, "actually.bmp.png")
	if err := os.WriteFile(bmpPath, encodeBMP(t, gradient(6, 6)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := carrier.Load(bmpPath); err != nil {
		t.Fatalf("Load BMP named .png: %v", err)
	}
}

func TestRejectAlphaBMP(t *testing.T) {
	// x/image only honours the alpha byte of a 32-bit BMP when the DIB header is
	// larger than the classic 40-byte BITMAPINFOHEADER; with the 40-byte header it
	// forces every alpha byte to 255. The encoder writes the 40-byte header, so a
	// transparent BMP has to be built by hand to exercise the refusal at all.
	raw := alphaBMP32(4, 4, 200)
	path := writeBMPFile(t, raw)

	if _, err := carrier.Load(path); !errors.Is(err, carrier.ErrAlpha) {
		t.Fatalf("Load error = %v, want ErrAlpha", err)
	}

	// Open is the same decode without the refusal, for the reporting commands.
	c, err := carrier.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !c.Alpha {
		t.Error("Alpha = false, want true")
	}
	if c.Image.Opaque() {
		t.Error("normalised image is opaque, want the transmitted alpha preserved")
	}
}

func TestBMP32WithoutAlphaInfoHeaderIsOpaque(t *testing.T) {
	// The counterpart to the test above: the decoder ignores the fourth byte of
	// a 40-byte-header 32-bit BMP and reports the image as opaque. Load must then
	// accept it, so the two paths cannot be confused for one another.
	raw := opaqueBMP32(4, 4)

	c, err := carrier.Load(writeBMPFile(t, raw))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Alpha {
		t.Error("Alpha = true, want the decoder to treat the image as opaque")
	}
}

func TestRejectTooLargeBMPBeforeDecode(t *testing.T) {
	// A valid header announcing a size past the pixel guard, with no pixel data
	// behind it. Getting ErrTooLarge proves the guard ran from the header alone
	// and never tried to allocate the image.
	raw := headerOnlyBMP(20000, 20000)
	path := writeBMPFile(t, raw)

	_, err := carrier.Load(path)
	if !errors.Is(err, carrier.ErrTooLarge) {
		t.Fatalf("Load error = %v, want ErrTooLarge", err)
	}
}

func TestRejectMalformedBMP(t *testing.T) {
	// BM signature with a truncated header: sniff passes, so the error must name
	// BMP rather than the general unsupported-format case.
	path := writeBMPFile(t, []byte{'B', 'M', 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3})

	_, err := carrier.Load(path)
	if !errors.Is(err, carrier.ErrNotBMP) {
		t.Fatalf("Load error = %v, want ErrNotBMP", err)
	}
}

func TestRejectNonImageForBMPName(t *testing.T) {
	// Random bytes with a .bmp name is still not an image.
	path := filepath.Join(t.TempDir(), "junk.bmp")
	if err := os.WriteFile(path, []byte("not an image at all"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := carrier.Load(path)
	if !errors.Is(err, carrier.ErrNotImage) {
		t.Fatalf("Load error = %v, want ErrNotImage", err)
	}
}

func TestSaveBMPRejectsAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 128
	}

	out := filepath.Join(t.TempDir(), "out.bmp")
	if err := carrier.Save(out, img, nil); !errors.Is(err, carrier.ErrAlpha) {
		t.Fatalf("Save error = %v, want ErrAlpha", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("Save created a file despite rejecting the image")
	}
}

func TestSaveBMPRejectsEmpty(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.bmp")
	if err := carrier.Save(out, image.NewNRGBA(image.Rect(0, 0, 0, 0)), nil); !errors.Is(err, carrier.ErrEmpty) {
		t.Fatalf("Save error = %v, want ErrEmpty", err)
	}
}

func TestSaveBMPRejectsNil(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.bmp")
	if err := carrier.Save(out, nil, nil); !errors.Is(err, carrier.ErrEmpty) {
		t.Fatalf("Save error = %v, want ErrEmpty", err)
	}
}

// --- helpers ---

func encodeBMP(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, img); err != nil {
		t.Fatalf("encode BMP: %v", err)
	}
	return buf.Bytes()
}

func writeBMPFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "carrier.bmp")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write carrier: %v", err)
	}
	return path
}

// headerOnlyBMP builds a 24-bit BMP header with no pixel data. The reader
// accepts it at config time, which is what the size guard sees.
func headerOnlyBMP(w, h uint32) []byte {
	const (
		fileHeaderSize = 14
		dibHeaderSize  = 40
	)
	buf := make([]byte, fileHeaderSize+dibHeaderSize)
	buf[0], buf[1] = 'B', 'M'
	putU32LE(buf[2:], uint32(len(buf))) // file size
	putU32LE(buf[10:], fileHeaderSize+dibHeaderSize)
	putU32LE(buf[14:], dibHeaderSize)
	putU32LE(buf[18:], w)
	putU32LE(buf[22:], h)
	putU16LE(buf[26:], 1)  // planes
	putU16LE(buf[28:], 24) // bits per pixel
	putU32LE(buf[30:], 0)  // BI_RGB, uncompressed
	return buf
}

// bmp32 writes a 32-bit BMP from a BGRA pixel stream. dibSize selects the DIB
// header size, which is what decides whether x/image honours the alpha byte.
func bmp32(w, h int, dibSize uint32, strideBytes int, encode func(buf []byte)) []byte {
	fileHeaderSize := uint32(14)
	pixels := strideBytes * h
	buf := make([]byte, fileHeaderSize+dibSize+uint32(pixels))
	buf[0], buf[1] = 'B', 'M'
	putU32LE(buf[2:], uint32(len(buf)))
	putU32LE(buf[10:], fileHeaderSize+dibSize)
	putU32LE(buf[14:], dibSize)
	putU32LE(buf[18:], uint32(w))
	putU32LE(buf[22:], uint32(h))
	putU16LE(buf[26:], 1)  // planes
	putU16LE(buf[28:], 32) // bits per pixel
	putU32LE(buf[30:], 0)  // BI_RGB, uncompressed
	encode(buf[fileHeaderSize+dibSize:])
	return buf
}

// alphaBMP32 builds a 32-bit BMP with a V4 header, so the alpha byte survives
// the decoder, every pixel set to the given alpha.
func alphaBMP32(w, h int, alpha uint8) []byte {
	const dibV4Size = 108 // BITMAPV4HEADER
	return bmp32(w, h, dibV4Size, w*4, func(pix []byte) {
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i+0] = 30 // B
			pix[i+1] = 20 // G
			pix[i+2] = 10 // R
			pix[i+3] = alpha
		}
	})
}

// opaqueBMP32 builds a 32-bit BMP with the classic 40-byte header, whose fourth
// byte the decoder discards.
func opaqueBMP32(w, h int) []byte {
	const dibSize = 40 // BITMAPINFOHEADER
	return bmp32(w, h, dibSize, w*4, func(pix []byte) {
		for i := 0; i+3 < len(pix); i += 4 {
			pix[i+0] = 200
			pix[i+1] = 150
			pix[i+2] = 100
			pix[i+3] = 0 // ignored: the header is too small to carry alpha
		}
	})
}

func putU32LE(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putU16LE(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

// headerOnlyBMP builds a 24-bit BMP header with no pixel data. The reader
// accepts it at config time, which is what the size guard sees.
