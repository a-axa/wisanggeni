package carrier_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/carrier"
)

func TestLoadNormalizesPixels(t *testing.T) {
	img := gradient(16, 12)
	raw := encodePNG(t, img)

	// An opaque RGB PNG decodes as *image.RGBA, not NRGBA (§11.23). The
	// normalisation step is what makes the engine independent of that.
	decoded, kind, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if kind != "png" {
		t.Fatalf("decoded kind = %q, want png", kind)
	}
	if _, ok := decoded.(*image.RGBA); !ok {
		t.Fatalf("opaque RGB PNG decoded as %T, expected *image.RGBA", decoded)
	}

	c, err := carrier.Load(writePNGFile(t, raw))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got, want := c.Image.Rect, image.Rect(0, 0, 16, 12); got != want {
		t.Fatalf("Rect = %v, want %v", got, want)
	}
	if !bytes.Equal(c.Image.Pix, img.Pix) {
		t.Fatal("normalised pixels differ from the source image")
	}
	if len(c.Chunks) != 0 {
		t.Errorf("Chunks = %d, want 0", len(c.Chunks))
	}
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", c.Warnings)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	c, err := carrier.Load(writePNGFile(t, encodePNG(t, gradient(9, 7))))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.png")
	if err := carrier.Save(out, c.Image, c.Chunks); err != nil {
		t.Fatalf("Save: %v", err)
	}

	chunks := parseRawPNG(t, readFile(t, out))
	ihdr := firstChunk(t, chunks, "IHDR")
	if len(ihdr) != 13 {
		t.Fatalf("IHDR length = %d, want 13", len(ihdr))
	}
	if bitDepth, colourType := ihdr[8], ihdr[9]; bitDepth != 8 || colourType != 2 {
		t.Errorf("IHDR bit depth/colour type = %d/%d, want 8/2 (8-bit RGB)", bitDepth, colourType)
	}

	reloaded, err := carrier.Load(out)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !bytes.Equal(reloaded.Image.Pix, c.Image.Pix) {
		t.Fatal("pixels changed across Save and Load")
	}
}

func TestRejectAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = 10
		img.Pix[i+3] = 200
	}

	path := writePNGFile(t, encodePNG(t, img))
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
	if got, want := c.Image.Rect, image.Rect(0, 0, 4, 4); got != want {
		t.Errorf("Rect = %v, want %v", got, want)
	}
	if c.Image.Opaque() {
		t.Error("normalised image is opaque, want the transmitted alpha preserved")
	}
}

func TestOpenReportsOpaqueCarrier(t *testing.T) {
	c, err := carrier.Open(writePNGFile(t, encodePNG(t, gradient(8, 8))))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if c.Alpha {
		t.Error("Alpha = true for an opaque carrier")
	}
}

func TestRejectTooLargeBeforeDecode(t *testing.T) {
	// Header only: no IDAT at all, so a decode would fail. Getting ErrTooLarge
	// back proves the guard ran first and allocated nothing (§11.24).
	raw := headerOnlyPNG(20000, 20000)
	path := writePNGFile(t, raw)

	_, err := carrier.Load(path)
	if !errors.Is(err, carrier.ErrTooLarge) {
		t.Fatalf("Load error = %v, want ErrTooLarge", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := png.Decode(f); err == nil {
		t.Fatal("header-only file unexpectedly decoded; the test would not prove the guard")
	}
}

func TestRejectNonImage(t *testing.T) {
	pal := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{
		color.RGBA{255, 0, 0, 255},
		color.RGBA{0, 0, 255, 255},
	})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, pal, nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}

	// A GIF is neither PNG nor BMP, so the format sniff rejects it before any
	// decoder runs.
	_, err := carrier.Load(writePNGFile(t, buf.Bytes()))
	if !errors.Is(err, carrier.ErrNotImage) {
		t.Fatalf("Load error = %v, want ErrNotImage", err)
	}
	if errors.Is(err, carrier.ErrNotPNG) {
		t.Fatal("ErrNotImage should not also report ErrNotPNG; the formats are distinct")
	}
}

func TestRejectCorruptPNG(t *testing.T) {
	// A real PNG signature followed by a broken header: the sniff passes, so the
	// failure has to come from the PNG decoder and name PNG, not the general
	// unsupported-format error.
	raw := append([]byte{}, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}...)
	raw = append(raw, 0xff, 0xff, 0xff, 0xff, 'I', 'H', 'D', 'R')

	_, err := carrier.Load(writePNGFile(t, raw))
	if !errors.Is(err, carrier.ErrNotPNG) {
		t.Fatalf("Load error = %v, want ErrNotPNG", err)
	}
}

func TestChunkPreservation(t *testing.T) {
	base := encodePNG(t, gradient(8, 8))
	injected := []rawChunk{
		{"gAMA", []byte{0x00, 0x00, 0xB1, 0x8F}},
		{"cHRM", bytes.Repeat([]byte{0x00, 0x00, 0x00, 0x01}, 8)},
		{"sRGB", []byte{0x00}},
		{"pHYs", []byte{0x00, 0x00, 0x0B, 0x13, 0x00, 0x00, 0x0B, 0x13, 0x01}},
		{"tEXt", []byte("Comment\x00hidden")},
		{"eXIf", []byte{0x49, 0x49, 0x2A, 0x00}},
	}
	raw := injectAfterIHDR(t, base, injected...)

	c, err := carrier.Load(writePNGFile(t, raw))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantTypes := []string{"gAMA", "cHRM", "sRGB", "pHYs"}
	if len(c.Chunks) != len(wantTypes) {
		t.Fatalf("Chunks = %v, want %v", chunkTypesOf(c.Chunks), wantTypes)
	}
	for i, want := range wantTypes {
		if c.Chunks[i].Type != want {
			t.Errorf("Chunks[%d] = %q, want %q", i, c.Chunks[i].Type, want)
		}
	}

	for _, typ := range []string{"gAMA", "cHRM", "sRGB"} {
		if !anyContains(c.Warnings, typ) {
			t.Errorf("no warning mentions %s; warnings = %v", typ, c.Warnings)
		}
	}
	if anyContains(c.Warnings, "pHYs") {
		t.Errorf("pHYs should not warn; warnings = %v", c.Warnings)
	}

	out := filepath.Join(t.TempDir(), "out.png")
	if err := carrier.Save(out, c.Image, c.Chunks); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := parseRawPNG(t, readFile(t, out))

	var kept []string
	for _, chunk := range got {
		switch chunk.typ {
		case "gAMA", "cHRM", "sRGB", "pHYs":
			kept = append(kept, chunk.typ)
		case "tEXt", "eXIf":
			t.Errorf("%s chunk survived the writer", chunk.typ)
		}
	}
	if strings.Join(kept, ",") != strings.Join(wantTypes, ",") {
		t.Errorf("preserved chunks in output = %v, want %v", kept, wantTypes)
	}

	// Chunk data has to survive byte-for-byte.
	if !bytes.Equal(firstChunk(t, got, "gAMA"), injected[0].data) {
		t.Error("gAMA payload changed")
	}
}

func TestSaveDropsChunkTypesOutsidePolicy(t *testing.T) {
	img := gradient(4, 4)
	out := filepath.Join(t.TempDir(), "out.png")
	if err := carrier.Save(out, img, []carrier.Chunk{
		{Type: "tEXt", Data: []byte("Comment\x00secret")},
		{Type: "eXIf", Data: []byte{1, 2, 3}},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	for _, chunk := range parseRawPNG(t, readFile(t, out)) {
		if chunk.typ == "tEXt" || chunk.typ == "eXIf" {
			t.Fatalf("%s chunk was written", chunk.typ)
		}
	}
}

func TestGrayConverted(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	for i := range gray.Pix {
		gray.Pix[i] = 200
	}

	c, err := carrier.Load(writePNGFile(t, encodePNG(t, gray)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !anyContains(c.Warnings, "grayscale") {
		t.Errorf("Warnings = %v, want a grayscale notice", c.Warnings)
	}
	for i := 0; i < len(c.Image.Pix); i += 4 {
		if c.Image.Pix[i] != 200 || c.Image.Pix[i+1] != 200 || c.Image.Pix[i+2] != 200 {
			t.Fatalf("pixel %d = %v, want grey 200", i/4, c.Image.Pix[i:i+3])
		}
	}
}

func TestPalettedConverted(t *testing.T) {
	pal := color.Palette{
		color.RGBA{255, 0, 0, 255},
		color.RGBA{0, 0, 255, 255},
	}
	img := image.NewPaletted(image.Rect(0, 0, 4, 2), pal)
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 2)
	}

	c, err := carrier.Load(writePNGFile(t, encodePNG(t, img)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !anyContains(c.Warnings, "paletted") {
		t.Errorf("Warnings = %v, want a paletted notice", c.Warnings)
	}
	if got := c.Image.Pix[0:4]; !bytes.Equal(got, []byte{255, 0, 0, 255}) {
		t.Errorf("first pixel = %v, want red", got)
	}
}

func Test16BitDownsampled(t *testing.T) {
	img := image.NewNRGBA64(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(img.Pix); i += 8 {
		binary.BigEndian.PutUint16(img.Pix[i:], 0xABCD)
		binary.BigEndian.PutUint16(img.Pix[i+2:], 0xABCD)
		binary.BigEndian.PutUint16(img.Pix[i+4:], 0xABCD)
		binary.BigEndian.PutUint16(img.Pix[i+6:], 0xFFFF)
	}

	c, err := carrier.Load(writePNGFile(t, encodePNG(t, img)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !anyContains(c.Warnings, "16-bit") {
		t.Errorf("Warnings = %v, want a 16-bit notice", c.Warnings)
	}
	if got := c.Image.Pix[0]; got != 0xAB {
		t.Errorf("R = 0x%02X, want 0xAB", got)
	}
}

func TestSaveRejectsAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 128
	}

	out := filepath.Join(t.TempDir(), "out.png")
	if err := carrier.Save(out, img, nil); !errors.Is(err, carrier.ErrAlpha) {
		t.Fatalf("Save error = %v, want ErrAlpha", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("Save created a file despite rejecting the image")
	}
}

// --- helpers ---

type rawChunk struct {
	typ  string
	data []byte
}

func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = uint8(x * 17)
			img.Pix[i+1] = uint8(y * 23)
			img.Pix[i+2] = uint8(x*y + 7)
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

func writePNGFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "carrier.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write carrier: %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func parseRawPNG(t *testing.T, data []byte) []rawChunk {
	t.Helper()
	sig := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if len(data) < len(sig) || !bytes.Equal(data[:len(sig)], sig) {
		t.Fatal("data is not a PNG")
	}

	var chunks []rawChunk
	off := len(sig)
	for off+8 <= len(data) {
		n := int(binary.BigEndian.Uint32(data[off:]))
		typ := string(data[off+4 : off+8])
		start := off + 8
		if n < 0 || start+n+4 > len(data) {
			t.Fatalf("truncated %s chunk", typ)
		}
		chunks = append(chunks, rawChunk{typ: typ, data: data[start : start+n]})
		off = start + n + 4
		if typ == "IEND" {
			break
		}
	}
	return chunks
}

func buildPNG(chunks []rawChunk) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	for _, c := range chunks {
		var head [8]byte
		binary.BigEndian.PutUint32(head[:4], uint32(len(c.data)))
		copy(head[4:], c.typ)
		crc := crc32.NewIEEE()
		_, _ = crc.Write([]byte(c.typ))
		_, _ = crc.Write(c.data)

		buf.Write(head[:])
		buf.Write(c.data)
		var trailer [4]byte
		binary.BigEndian.PutUint32(trailer[:], crc.Sum32())
		buf.Write(trailer[:])
	}
	return buf.Bytes()
}

func injectAfterIHDR(t *testing.T, base []byte, inject ...rawChunk) []byte {
	t.Helper()
	var out []rawChunk
	for _, c := range parseRawPNG(t, base) {
		out = append(out, c)
		if c.typ == "IHDR" {
			out = append(out, inject...)
		}
	}
	return buildPNG(out)
}

func headerOnlyPNG(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // colour type: truecolour
	return buildPNG([]rawChunk{{typ: "IHDR", data: ihdr}})
}

func chunkTypesOf(chunks []carrier.Chunk) []string {
	types := make([]string, len(chunks))
	for i, c := range chunks {
		types[i] = c.Type
	}
	return types
}

func firstChunk(t *testing.T, chunks []rawChunk, typ string) []byte {
	t.Helper()
	for _, c := range chunks {
		if c.typ == typ {
			return c.data
		}
	}
	t.Fatalf("chunk %s not found", typ)
	return nil
}

func anyContains(values []string, substr string) bool {
	for _, v := range values {
		if strings.Contains(v, substr) {
			return true
		}
	}
	return false
}
