package carrier

import (
	"bytes"
	"fmt"
	"image"
	"os"

	"golang.org/x/image/bmp"
)

// saveBMP writes img to path as a 24-bit BMP.
//
// BMP has no ancillary chunks, so there is nothing to preserve and nothing to
// strip: the chunk policy of §7.7 simply does not apply. The x/image writer
// emits a plain 24-bit uncompressed bitmap with no palette, which is what makes
// the format lossless here. Every channel of img round-trips exactly, and no
// metadata the caller did not ask for is added.
//
// The file is created or truncated; callers that need atomic replacement write
// to a temporary path and rename afterwards, exactly as for PNG.
func saveBMP(path string, img *image.NRGBA) error {
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, img); err != nil {
		return fmt.Errorf("encode BMP: %w", err)
	}

	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		// The output was created by us and may now be incomplete; leaving it
		// behind would look like a usable carrier. os.WriteFile can fail after
		// truncating, so the removal is not redundant with the error above.
		_ = os.Remove(path)
		return err
	}
	return nil
}
