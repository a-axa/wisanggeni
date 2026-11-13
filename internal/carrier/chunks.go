package carrier

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"

	"github.com/a-axa/wisanggeni/internal/format"
)

// Chunk is one ancillary PNG chunk carried from the input to the output.
type Chunk struct {
	// Type is the four-byte chunk name, for example "gAMA".
	Type string

	// Data is the chunk payload without the length and CRC fields.
	Data []byte
}

// pngSignature is the eight-byte file header defined by the PNG specification.
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}

const (
	// maxPreservedChunkSize bounds how much ancillary data is retained. A
	// hostile file can claim a four-gigabyte chunk; an ICC profile that large
	// is not worth carrying.
	maxPreservedChunkSize = 8 << 20

	chunkTypeLen = 4
	chunkLenLen  = 4
	chunkCRCLen  = 4
)

var (
	// preserved reports whether a chunk type survives a round-trip (§7.7).
	preserved = typeSet(format.PreservedChunks)

	// colourManaged are the chunk types that change how pixels are displayed;
	// their presence is worth a warning.
	colourManaged = typeSet([]string{"gAMA", "cHRM", "sRGB", "iCCP"})
)

func typeSet(types []string) map[string]bool {
	set := make(map[string]bool, len(types))
	for _, t := range types {
		set[t] = true
	}
	return set
}

// readChunks streams a PNG and returns the preserved chunks in file order,
// together with one warning per colour-management chunk (§B1). Only preserved
// chunk payloads are buffered; everything else is discarded as it is read.
func readChunks(r io.Reader) ([]Chunk, []string, error) {
	sig := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(r, sig); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNotPNG, err)
	}
	if !bytes.Equal(sig, pngSignature) {
		return nil, nil, ErrNotPNG
	}

	var (
		chunks   []Chunk
		warnings []string
		head     [chunkLenLen + chunkTypeLen]byte
	)

	for {
		if _, err := io.ReadFull(r, head[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return chunks, warnings, nil
			}
			return nil, nil, fmt.Errorf("read PNG chunk header: %w", err)
		}

		length := binary.BigEndian.Uint32(head[:chunkLenLen])
		typ := string(head[chunkLenLen:])

		keep := preserved[typ]
		if keep && int64(length) > maxPreservedChunkSize {
			warnings = append(warnings,
				fmt.Sprintf("carrier %s chunk is larger than %d bytes and was dropped", typ, maxPreservedChunkSize))
			keep = false
		}

		if keep {
			data := make([]byte, length)
			if _, err := io.ReadFull(r, data); err != nil {
				return nil, nil, fmt.Errorf("read %s chunk: %w", typ, err)
			}
			chunks = append(chunks, Chunk{Type: typ, Data: data})
		} else if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil {
			return nil, nil, fmt.Errorf("skip %s chunk: %w", typ, err)
		}

		if colourManaged[typ] {
			warnings = append(warnings,
				fmt.Sprintf("carrier has a %s chunk; its rendered colours may differ from the stored pixels", typ))
		}

		if _, err := io.CopyN(io.Discard, r, chunkCRCLen); err != nil {
			return nil, nil, fmt.Errorf("read %s CRC: %w", typ, err)
		}
		if typ == "IEND" {
			return chunks, warnings, nil
		}
	}
}

// chunkRef points at a chunk inside an in-memory PNG, without copying it.
type chunkRef struct {
	typ  string
	data []byte
}

// indexChunks splits an in-memory PNG into chunk references.
func indexChunks(buf []byte) ([]chunkRef, error) {
	if len(buf) < len(pngSignature) || !bytes.Equal(buf[:len(pngSignature)], pngSignature) {
		return nil, ErrNotPNG
	}

	var refs []chunkRef
	off := len(pngSignature)
	for {
		if off+chunkLenLen+chunkTypeLen > len(buf) {
			return nil, fmt.Errorf("truncated chunk header at offset %d", off)
		}
		length := int(binary.BigEndian.Uint32(buf[off : off+chunkLenLen]))
		typ := string(buf[off+chunkLenLen : off+chunkLenLen+chunkTypeLen])
		start := off + chunkLenLen + chunkTypeLen
		end := start + length
		if length < 0 || end+chunkCRCLen > len(buf) {
			return nil, fmt.Errorf("truncated %s chunk at offset %d", typ, off)
		}
		refs = append(refs, chunkRef{typ: typ, data: buf[start:end]})
		off = end + chunkCRCLen
		if typ == "IEND" {
			return refs, nil
		}
	}
}

// Save writes img to path as an 8-bit RGB PNG, keeping chunks in their
// original order and recomputing every CRC (§7.7). The file is created or
// truncated; callers that need atomic replacement write to a temporary path
// and rename afterwards.
//
// Chunk types outside format.PreservedChunks are ignored, so a caller cannot
// smuggle EXIF or text back into the output.
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

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return fmt.Errorf("encode PNG: %w", err)
	}
	refs, err := indexChunks(encoded.Bytes())
	if err != nil {
		return fmt.Errorf("read encoded PNG: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	writeErr := writePreserving(w, refs, chunks)
	if flushErr := w.Flush(); writeErr == nil {
		writeErr = flushErr
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		// The output was created by us and is now incomplete; leaving it
		// behind would look like a usable carrier.
		_ = os.Remove(path)
		return writeErr
	}
	return nil
}

// writePreserving emits signature, IHDR, the preserved carrier chunks, the
// encoder's IDAT chunks and IEND, in that order.
func writePreserving(w io.Writer, encoded []chunkRef, chunks []Chunk) error {
	if _, err := w.Write(pngSignature); err != nil {
		return err
	}

	var ihdr *chunkRef
	for i := range encoded {
		if encoded[i].typ == "IHDR" {
			ihdr = &encoded[i]
			break
		}
	}
	if ihdr == nil {
		return errors.New("PNG encoder produced no IHDR")
	}
	if err := writeChunk(w, ihdr.typ, ihdr.data); err != nil {
		return err
	}

	for _, c := range chunks {
		if !preserved[c.Type] {
			continue
		}
		if err := writeChunk(w, c.Type, c.Data); err != nil {
			return err
		}
	}

	for _, ref := range encoded {
		switch ref.typ {
		case "IDAT", "IEND":
			if err := writeChunk(w, ref.typ, ref.data); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeChunk writes one PNG chunk with a freshly computed CRC.
func writeChunk(w io.Writer, typ string, data []byte) error {
	if len(typ) != chunkTypeLen {
		return fmt.Errorf("invalid PNG chunk type %q", typ)
	}

	var head [chunkLenLen + chunkTypeLen]byte
	binary.BigEndian.PutUint32(head[:chunkLenLen], uint32(len(data)))
	copy(head[chunkLenLen:], typ)

	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(typ))
	_, _ = crc.Write(data)

	var trailer [chunkCRCLen]byte
	binary.BigEndian.PutUint32(trailer[:], crc.Sum32())

	for _, part := range [][]byte{head[:], data, trailer[:]} {
		if _, err := w.Write(part); err != nil {
			return err
		}
	}
	return nil
}
