package carrier

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"strings"
)

// Format names carried by sniff and decode. They match the names registered
// with image.RegisterFormat, so a mismatch between the magic bytes and the
// registered decoder is detectable.
const (
	formatPNG = "png"
	formatBMP = "bmp"
)

// Sniffed file headers. BMP recognition mirrors what image.DecodeConfig can
// see: the reader registers "BM????\x00\x00\x00\x00", so the two signature
// bytes are followed by a wildcard file size and then four bytes that must be
// zero. Sniffing anything wider would let a file past this check only to fail
// in DecodeConfig with a vaguer error.
var bmpSignature = []byte{'B', 'M'}

// Bitmap file header offsets, per the BMP specification: two signature bytes,
// a four-byte file size, then four reserved bytes that must be zero.
const (
	bmpReservedStart = 6
	bmpReservedEnd   = 10
)

var (
	// ErrNotImage reports a file that is neither PNG nor BMP.
	ErrNotImage = fmt.Errorf("%w: expected a PNG or a BMP image", ErrUnsupportedFormat)
)

// sniff identifies a carrier format from its leading bytes, without decoding
// the rest of the file. It reads from r and leaves the position undefined.
func sniff(r io.Reader) (string, error) {
	head := make([]byte, len(pngSignature))
	n, err := io.ReadFull(r, head)
	if err != nil && n < len(bmpSignature) {
		return "", fmt.Errorf("%w: read header: %w", ErrNotImage, err)
	}

	if n == len(pngSignature) && bytes.Equal(head, pngSignature) {
		return formatPNG, nil
	}
	if n >= len(bmpSignature) && bytes.Equal(head[:len(bmpSignature)], bmpSignature) && reservedZero(head) {
		return formatBMP, nil
	}
	return "", ErrNotImage
}

// reservedZero reports whether the four reserved bytes of a BMP header are
// zero, which is what the registered magic requires. A short read counts as
// zero, because the decoder is the one that decides whether the file is long
// enough.
func reservedZero(head []byte) bool {
	for i := bmpReservedStart; i < bmpReservedEnd && i < len(head); i++ {
		if head[i] != 0 {
			return false
		}
	}
	return true
}

// Save writes img to path. The format follows the file extension: .bmp writes a
// BMP, anything else writes a PNG. Chunks are carried over to a PNG output and
// ignored for a BMP, which has no ancillary chunks.
//
// A PNG output keeps the chunk policy of §7.7 and recomputes every CRC; a BMP
// output is written losslessly, with no palette tricks, so every channel
// round-trips exactly.
//
// Verification is deliberately not part of this call: internal/hide reopens the
// output and compares the embedded blob, which is a stronger check than
// re-reading the pixels would be. Keeping it there leaves this package with one
// job.
func Save(path string, img *image.NRGBA, chunks []Chunk) error {
	if img == nil {
		return ErrEmpty
	}
	bounds := img.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return ErrEmpty
	}
	if !img.Opaque() {
		return ErrAlpha
	}

	if strings.EqualFold(filepath.Ext(path), ".bmp") {
		return saveBMP(path, img)
	}
	return savePNG(path, img, chunks)
}
