// Package inspect answers what a PNG is and what it hides (§7.10). It is the one
// implementation of the report: the CLI formats what it returns as lines, and the
// TUI draws the same fields as rows, so the two surfaces cannot disagree.
package inspect

import (
	"image"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/reveal"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// Info is the carrier layer: everything that can be reported without a
// passphrase, and nothing that depends on the payload.
type Info struct {
	Width  int
	Height int

	// Channels is the number of colour channels, the unit every capacity below
	// is expressed in.
	Channels int64

	// Raw, Usable, Suggested and Strong are the four §6.1 capacity numbers.
	Raw       int64
	Usable    int64
	Suggested int64
	Strong    int64

	// Alpha reports a carrier the engine would refuse to embed in.
	Alpha bool

	// Scored reports whether Score holds a real measurement. A caller that will
	// not display the score leaves it false and skips an O(pixels) pass.
	Scored bool

	// Score is the §7.5 cover classification.
	Score stego.CoverStats

	// Chunks are the preserved colour chunk names, in file order.
	Chunks []string

	// Warnings are the decoder's advisory findings: colour chunks, lossy
	// conversions. The CLI writes them to stderr; the TUI shows them on screen.
	Warnings []string
}

// Describe fills the carrier layer of the report. score asks for the cover
// classification as well; the reporter that prints the numbers passes true, and
// the one that only needs capacity passes false rather than paying for a second
// pass over every pixel.
func Describe(car *carrier.Carrier, score bool) Info {
	width, height := car.Image.Rect.Dx(), car.Image.Rect.Dy()
	info := Info{
		Width:     width,
		Height:    height,
		Channels:  stego.Channels(width, height),
		Raw:       stego.CapacityRaw(width, height),
		Usable:    stego.Capacity(width, height),
		Suggested: stego.SuggestedCapacity(width, height),
		Strong:    stego.StrongCapacity(width, height),
		Alpha:     car.Alpha,
		Chunks:    chunkTypes(car.Chunks),
		Warnings:  append([]string(nil), car.Warnings...),
	}
	if score {
		info.Score = stego.CoverScore(car.Image)
		info.Scored = true
	}
	return info
}

// Payload is the layer a passphrase unlocks: §7.9 without the extraction to
// disk. It exists only when the blob authenticated.
type Payload struct {
	Version    uint8
	Compressed bool
	BodyLen    uint64

	// HeaderOnly records that the caller stopped after the header, so Entries is
	// empty by request rather than by failure.
	HeaderOnly bool

	// Entries are the archive members, in archive order.
	Entries []archive.Entry

	// Files is the number of non-directory entries.
	Files int

	// Total is the uncompressed size of those files.
	Total int64
}

// chunkTypes names the preserved chunks.
func chunkTypes(chunks []carrier.Chunk) []string {
	if len(chunks) == 0 {
		return nil
	}
	types := make([]string, len(chunks))
	for i, c := range chunks {
		types[i] = c.Type
	}
	return types
}

// OpenPayload runs the decrypt path of §7.9 against an already decoded carrier
// and reports what the blob holds. Every failure before the archive parse
// collapses into the same opaque error as reveal, so inspect cannot be used as an
// oracle. Nothing is written to disk.
func OpenPayload(img *image.NRGBA, passphrase []byte, headerOnly bool) (*Payload, error) {
	salt, err := stego.ExtractSalt(img, format.SaltSize)
	if err != nil {
		return nil, reveal.DecryptError()
	}
	keys, err := crypto.Derive(string(passphrase), salt)
	if err != nil {
		return nil, err
	}

	prefix, err := reveal.ExtractAt(img, keys.PosSeed, 0, reveal.PayloadPrefix)
	if err != nil {
		return nil, reveal.DecryptError()
	}
	headerNonce := prefix[:format.NonceSize]
	headerCT := prefix[format.NonceSize : format.NonceSize+format.HeaderCipherSize]
	bodyNonce := prefix[format.NonceSize+format.HeaderCipherSize:]

	header, err := crypto.OpenHeader(keys.EncKey, salt, headerNonce, headerCT)
	if err != nil {
		return nil, reveal.DecryptError()
	}

	// Validate the authenticated body length against the carrier before it is
	// turned into a slice, then reject a payload this writer cannot read.
	width, height := img.Rect.Dx(), img.Rect.Dy()
	if header.BodyLen > uint64(stego.Capacity(width, height)) {
		return nil, reveal.DecryptError()
	}
	if header.Version != format.Version || header.Flags&^format.FlagZstd != 0 {
		return nil, exitcode.Fail(exitcode.Decrypt,
			"payload was written by an incompatible writer (v%d, flags %#02x)",
			header.Version, header.Flags)
	}

	payload := &Payload{
		Version:    header.Version,
		Compressed: header.Flags&format.FlagZstd != 0,
		BodyLen:    header.BodyLen,
		HeaderOnly: headerOnly,
	}
	if headerOnly {
		return payload, nil
	}

	bodyCT, err := reveal.ExtractAt(img, keys.PosSeed, reveal.PayloadPrefix, int(header.BodyLen)+format.TagSize)
	if err != nil {
		return nil, reveal.DecryptError()
	}
	plain, err := crypto.OpenBody(keys.EncKey, headerCT, bodyNonce, bodyCT)
	if err != nil {
		return nil, reveal.DecryptError()
	}
	defer clear(plain)

	// List rather than Unpack: the names are the point, and nothing may be
	// written to disk on the way to reporting them.
	entries, err := archive.List(plain, archive.ListOptions{Compressed: payload.Compressed})
	if err != nil {
		return nil, reveal.UnpackError(err)
	}
	payload.Entries = entries
	for _, entry := range entries {
		if !entry.Dir {
			payload.Files++
			payload.Total += entry.Size
		}
	}
	return payload, nil
}
