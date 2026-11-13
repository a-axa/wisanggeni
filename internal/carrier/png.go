// Package carrier loads PNG carriers, normalises them to 8-bit NRGBA, and
// writes the stego image back while preserving the colour-management chunks
// (§7.7, §11.23-26).
//
// Everything here runs before any stego or crypto code, so failures stay easy
// to localise: a carrier is either acceptable or rejected with a specific
// reason.
package carrier

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"

	"github.com/a-axa/wisanggeni/internal/format"
)

var (
	// ErrNotPNG reports a file that is not a PNG image.
	ErrNotPNG = errors.New("carrier is not a PNG image")

	// ErrTooLarge reports dimensions the format cannot address. It is raised
	// from the header alone, before any pixel buffer is allocated.
	ErrTooLarge = errors.New("carrier is too large")

	// ErrAlpha reports a carrier with transparent pixels. Embedding into
	// partially transparent pixels loses bits, because viewers and tools
	// rewrite the colour of invisible pixels (§11.25).
	ErrAlpha = errors.New("carrier is not opaque; alpha carriers are not supported")

	// ErrEmpty reports a carrier with no pixels.
	ErrEmpty = errors.New("carrier has no pixels")
)

// Carrier is a decoded carrier ready for embedding.
type Carrier struct {
	// Image is the normalised 8-bit RGBA image with Rect.Min at (0,0).
	Image *image.NRGBA

	// Chunks are the colour chunks to carry over to the output, in the order
	// they appeared in the input.
	Chunks []Chunk

	// Warnings are advisory findings: colour chunks, and lossy conversions
	// from 16-bit, paletted or grayscale input.
	Warnings []string
}

// Load decodes the carrier at path.
//
// The header is read first so that oversized images are rejected before a
// pixel buffer exists (§11.24). The returned image is always *image.NRGBA with
// Rect.Min = (0,0), whatever the file's colour type was, so callers never have
// to think about RGBA vs NRGBA or about stride offsets.
func Load(path string) (*Carrier, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	cfg, kind, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("read carrier header: %w", err)
	}
	if kind != "png" {
		return nil, fmt.Errorf("%w: decoded as %q", ErrNotPNG, kind)
	}
	if err := checkDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	chunks, chunkWarnings, err := readChunks(f)
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode carrier: %w", err)
	}

	pix, convertWarnings, err := normalize(src)
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0, len(chunkWarnings)+len(convertWarnings))
	warnings = append(warnings, chunkWarnings...)
	warnings = append(warnings, convertWarnings...)

	return &Carrier{Image: pix, Chunks: chunks, Warnings: warnings}, nil
}

// checkDimensions enforces the two limits that have to hold before decoding:
// the pixel guard, and the uint32 channel index the format stores.
func checkDimensions(w, h int) error {
	if w <= 0 || h <= 0 {
		return ErrEmpty
	}
	if int64(w)*int64(h) > format.MaxPixels {
		return fmt.Errorf("%w: %dx%d exceeds the %d pixel limit", ErrTooLarge, w, h, format.MaxPixels)
	}
	if int64(w)*int64(h)*3 > int64(format.MaxChannelIndex) {
		return fmt.Errorf("%w: %dx%d exceeds %d channels", ErrTooLarge, w, h, int64(format.MaxChannelIndex))
	}
	return nil
}

// normalize converts any decoded image into the one shape the engine accepts.
func normalize(src image.Image) (*image.NRGBA, []string, error) {
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, nil, ErrEmpty
	}

	// Reject transparency before allocating anything.
	if o, ok := src.(interface{ Opaque() bool }); ok && !o.Opaque() {
		return nil, nil, ErrAlpha
	}

	var warnings []string
	switch src.(type) {
	case *image.NRGBA, *image.RGBA:
		// Already 8-bit truecolour; nothing is lost.
	case *image.NRGBA64, *image.RGBA64:
		warnings = append(warnings, "carrier is 16-bit; downsampled to 8 bits per channel")
	case *image.Paletted:
		warnings = append(warnings, "carrier is paletted; expanded to 8-bit RGB")
	case *image.Gray, *image.Gray16:
		warnings = append(warnings, "carrier is grayscale; converted to 8-bit RGB")
	}

	dst := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Src)

	// The type switch above cannot see through every colour model, so the
	// converted result is checked too.
	if !dst.Opaque() {
		return nil, nil, ErrAlpha
	}
	return dst, warnings, nil
}
