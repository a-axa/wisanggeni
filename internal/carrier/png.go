// Package carrier loads PNG and BMP carriers, normalises them to 8-bit NRGBA,
// and writes the stego image back. PNG output preserves the colour-management
// chunks (§7.7, §11.23-26); BMP has no ancillary chunks, so there is nothing to
// carry over.
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

	"golang.org/x/image/bmp"

	"github.com/a-axa/wisanggeni/internal/format"
)

var (
	// ErrNotPNG reports a file that is not a PNG image.
	ErrNotPNG = errors.New("carrier is not a PNG image")

	// ErrNotBMP reports a file that is not a BMP image.
	ErrNotBMP = errors.New("carrier is not a BMP image")

	// ErrUnsupportedFormat reports a carrier whose format is recognised but not
	// supported. Today that means anything that is neither PNG nor BMP.
	ErrUnsupportedFormat = errors.New("carrier format is not supported")

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

	// Alpha reports that at least one pixel is not opaque. Load refuses such a
	// carrier; Open returns it so a reporting command can say why (§5, §7.10).
	Alpha bool
}

// Load decodes the carrier at path, which may be a PNG or a BMP. The format is
// sniffed from the content, not the extension.
//
// The header is read first so that oversized images are rejected before a
// pixel buffer exists (§11.24), and a carrier with a transparent pixel is
// refused with ErrAlpha before its pixels are converted (§11.25). The returned
// image is always *image.NRGBA with Rect.Min = (0,0), whatever the file's
// colour type was, so callers never have to think about RGBA vs NRGBA or about
// stride offsets.
func Load(path string) (*Carrier, error) {
	return open(path, false)
}

// Open decodes the carrier at path exactly like Load, except that it tolerates
// transparency and reports it through Carrier.Alpha instead of failing. Only the
// commands that report on a file use it, so they can describe a carrier the
// engine would refuse; every embedding path needs Load.
func Open(path string) (*Carrier, error) {
	return open(path, true)
}

// open is the shared decoder behind Load and Open. allowAlpha lets a transparent
// carrier through instead of returning ErrAlpha.
//
// The format is sniffed from the file's magic bytes rather than trusted from the
// extension, so a PNG renamed to .bmp (or the reverse) still decodes as what it
// actually is, and a file that is neither is rejected with a specific error.
func open(path string, allowAlpha bool) (*Carrier, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	kind, err := sniff(f)
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	cfg, decoded, err := image.DecodeConfig(f)
	if err != nil {
		// A file that sniffed as PNG but whose header the decoder rejects is a
		// broken PNG, not an unsupported format; say so, so the caller learns
		// which format was being read.
		return nil, fmt.Errorf("%w: %w", notKindError(kind), err)
	}
	if decoded != kind {
		return nil, fmt.Errorf("%w: header says %q, content is %q", ErrUnsupportedFormat, kind, decoded)
	}
	if err := checkDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, err
	}

	var (
		chunks   []Chunk
		warnings []string
	)
	if kind == formatPNG {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		chunks, warnings, err = readChunks(f)
		if err != nil {
			return nil, err
		}
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, err := decode(f, kind)
	if err != nil {
		return nil, err
	}

	// Refuse transparency before converting anything when the caller cannot take
	// it; the decode above already produced the pixels, but the conversion and
	// its buffer do not have to happen.
	alpha := false
	if !isOpaque(src) {
		if !allowAlpha {
			return nil, ErrAlpha
		}
		alpha = true
	}

	pix, convertWarnings, err := normalize(src)
	if err != nil {
		return nil, err
	}
	// The check above cannot see through every colour model, so the converted
	// result is checked too.
	if !pix.Opaque() {
		if !allowAlpha {
			return nil, ErrAlpha
		}
		alpha = true
	}

	all := make([]string, 0, len(warnings)+len(convertWarnings))
	all = append(all, warnings...)
	all = append(all, convertWarnings...)

	return &Carrier{Image: pix, Chunks: chunks, Warnings: all, Alpha: alpha}, nil
}

// decode dispatches to the decoder for a sniffed format.
func decode(r io.Reader, kind string) (image.Image, error) {
	var (
		src image.Image
		err error
	)
	switch kind {
	case formatPNG:
		src, err = png.Decode(r)
	case formatBMP:
		src, err = bmp.Decode(r)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedFormat, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("decode carrier: %w", fmt.Errorf("%w: %w", notKindError(kind), err))
	}
	return src, nil
}

// notKindError maps a sniffed format to the error that names it, so a caller
// can tell a broken PNG from a broken BMP.
func notKindError(kind string) error {
	switch kind {
	case formatPNG:
		return ErrNotPNG
	case formatBMP:
		return ErrNotBMP
	default:
		return ErrUnsupportedFormat
	}
}

// isOpaque reports a decoded image's transparency when its concrete type can
// answer cheaply. An unknown type counts as opaque and is re-checked after
// normalisation.
func isOpaque(img image.Image) bool {
	o, ok := img.(interface{ Opaque() bool })
	return !ok || o.Opaque()
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
// Lossy conversions are reported as warnings; transparency is not its concern,
// because Load and Open decide what to do about it.
func normalize(src image.Image) (*image.NRGBA, []string, error) {
	bounds := src.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, nil, ErrEmpty
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
	return dst, warnings, nil
}
