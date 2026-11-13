// Package reveal runs the extraction pipeline of §7.9.
//
// It is the single implementation behind `wisanggeni reveal`, `inspect`'s payload
// layer and the TUI, so every reader agrees on what a blob is. Nothing here
// reads a terminal: the caller owns the passphrase.
//
// Every failure before the archive parse collapses into one opaque error
// (DecryptError) on purpose. A wrong passphrase, an image with nothing hidden in
// it and a damaged blob are indistinguishable to a reader, and telling them apart
// would hand an attacker an oracle (§11.1).
package reveal

import (
	"errors"
	"image"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// PayloadPrefix is the part of the payload a reader must have before it can
// decrypt the header: header_nonce | header_ct | body_nonce (§6).
const PayloadPrefix = format.BodyCipherOffset - format.SaltSize

// Options describes one extraction.
type Options struct {
	// Dir is the destination directory. It is created when missing.
	Dir string

	// Force allows replacing existing files.
	Force bool

	// MaxOutput caps the decompressed size. Zero or negative means the archive
	// package's default cap; there is no way to lift it, so a payload cannot
	// expand without bound (docs/FORMAT.md §13.1).
	MaxOutput int64

	// Passphrase must be the one the blob was sealed with.
	Passphrase []byte
}

// Result reports what Open wrote.
type Result struct {
	// Dir is the destination directory.
	Dir string

	// Entries lists the members in archive order.
	Entries []archive.Entry

	// Paths lists the absolute paths written, in the same order.
	Paths []string

	// Total is the sum of the file sizes.
	Total int64
}

// Open decrypts the blob hidden in img and unpacks it into opts.Dir.
func Open(img *image.NRGBA, opts Options) (Result, error) {
	// Step 3: the salt sits at public positions, so it is readable without a key.
	salt, err := stego.ExtractSalt(img, format.SaltSize)
	if err != nil {
		return Result{}, DecryptError()
	}
	keys, err := crypto.Derive(string(opts.Passphrase), salt)
	if err != nil {
		return Result{}, err
	}

	// Step 5: the fixed prefix holds both nonces and the header ciphertext, which
	// is everything needed before a body_len can be trusted or allocated.
	prefix, err := ExtractAt(img, keys.PosSeed, 0, PayloadPrefix)
	if err != nil {
		return Result{}, DecryptError()
	}
	headerNonce := prefix[:format.NonceSize]
	headerCT := prefix[format.NonceSize : format.NonceSize+format.HeaderCipherSize]
	bodyNonce := prefix[format.NonceSize+format.HeaderCipherSize:]

	header, err := crypto.OpenHeader(keys.EncKey, salt, headerNonce, headerCT)
	if err != nil {
		return Result{}, DecryptError()
	}

	// Step 7: validate the authenticated body length against the carrier before
	// it is turned into a slice or an allocation.
	width, height := img.Rect.Dx(), img.Rect.Dy()
	if header.BodyLen > uint64(stego.Capacity(width, height)) {
		return Result{}, DecryptError()
	}
	// Version and flag bits live inside the authenticated header, so reporting
	// them to the key holder leaks nothing.
	if header.Version != format.Version || header.Flags&^format.FlagZstd != 0 {
		return Result{}, exitcode.Fail(exitcode.Decrypt,
			"payload was written by an incompatible writer (v%d, flags %#02x)",
			header.Version, header.Flags)
	}

	// Step 8: the body continues the same pool, so its bits follow the prefix's.
	bodyCT, err := ExtractAt(img, keys.PosSeed, PayloadPrefix, int(header.BodyLen)+format.TagSize)
	if err != nil {
		return Result{}, DecryptError()
	}
	plain, err := crypto.OpenBody(keys.EncKey, headerCT, bodyNonce, bodyCT)
	if err != nil {
		return Result{}, DecryptError()
	}

	// Step 10.
	unpacked, err := archive.Unpack(plain, opts.Dir, archive.UnpackOptions{
		Compressed: header.Flags&format.FlagZstd != 0,
		MaxBytes:   opts.MaxOutput,
		Force:      opts.Force,
	})
	if err != nil {
		return Result{}, UnpackError(err)
	}

	var total int64
	for _, entry := range unpacked.Entries {
		total += entry.Size
	}
	return Result{
		Dir:     opts.Dir,
		Entries: unpacked.Entries,
		Paths:   unpacked.Paths,
		Total:   total,
	}, nil
}

// ExtractAt reads size payload bytes starting at byte offset off of the STC
// stream. Every message byte is one independent block of STCWidth channels, and
// the offsets used here are multiples of STCWidth, so reading the prefix and the
// body as separate slices lands on the same block boundaries the writer used.
func ExtractAt(img *image.NRGBA, posSeed [32]byte, off, size int) ([]byte, error) {
	if size <= 0 {
		return nil, nil
	}
	end := off + size
	positions := stego.PoolPositions(stego.ChannelCount(img), posSeed, end*stego.STCWidth)
	if len(positions) < end*stego.STCWidth {
		return nil, stego.ErrTooSmall
	}
	return stego.ExtractSTC(img, positions[off*stego.STCWidth:], size)
}

// DecryptError is the single failure of the decrypt path (§5, exit 3). Callers
// must not refine it: the ambiguity is the point.
func DecryptError() error {
	return exitcode.Fail(exitcode.Decrypt,
		"cannot decrypt: wrong passphrase, or the image contains no hidden data")
}

// UnpackError maps the archive refusals onto the documented exit codes. Only the
// destination collision has a remedy the user can apply, so it gets a hint.
func UnpackError(err error) error {
	switch {
	case errors.Is(err, archive.ErrExists):
		return exitcode.Fail(exitcode.Error, "%v; use --force to overwrite", err)
	case errors.Is(err, archive.ErrTooLarge):
		return exitcode.Fail(exitcode.Error, "%v; raise --max-output to allow more", err)
	case errors.Is(err, archive.ErrUnsafePath):
		return exitcode.Fail(exitcode.Error, "refusing to write outside the output directory: %v", err)
	default:
		return err
	}
}
