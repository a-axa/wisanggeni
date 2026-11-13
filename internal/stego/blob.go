package stego

import (
	"bytes"
	"errors"
	"fmt"
	"image"

	"github.com/a-axa/wisanggeni/internal/format"
)

// ErrVerify reports a carrier whose embedded blob no longer matches what was
// written: the writer's self-check of §7.8 step 14.
var ErrVerify = errors.New("stego: verification failed")

// EmbedBlob writes a whole container blob into a normalised carrier without any
// crypto (§7.8 steps 10-12).
//
// The blob's first format.SaltSize bytes are the salt: they go to the public
// positions S with LSB matching, which needs no key, so a reader can recover
// them first. Every remaining byte is written through STC over the keyed pool
// P', so posSeed must be the key's position seed. costs is the cover's cost map.
//
// blob must be at least format.SaltSize bytes and the carrier must have room for
// the salt plus STCWidth channels per payload byte; see CapacityRaw for the
// maximum.
//
// The LSB-matching direction stream is derived from blob, so the embed is a pure
// function of (cover, blob, posSeed, costs): the same container lands on the same
// pixels every time, which is what makes two writers comparable byte for byte
// (§6, §7.6).
func EmbedBlob(img *image.NRGBA, blob []byte, posSeed [32]byte, costs []float64) error {
	channels := ChannelCount(img)
	if channels == 0 {
		return ErrTooSmall
	}
	if len(blob) < format.SaltSize {
		return fmt.Errorf("%w: %d-byte blob is shorter than the %d-byte salt",
			ErrTooSmall, len(blob), format.SaltSize)
	}

	// One direction stream for the whole blob, so the salt and the payload cannot
	// desynchronise from each other.
	flip := blobFlip(blob)

	// §7.8 step 11: salt first, at S.
	if err := embedSalt(img, blob[:format.SaltSize], flip); err != nil {
		return err
	}

	// §7.8 step 12: the rest through STC over P'. PoolPositions excludes S, so
	// the two regions cannot overlap, and it clamps to the pool size, which
	// EmbedSTC then reports as too small.
	payload := blob[format.SaltSize:]
	positions := PoolPositions(channels, posSeed, len(payload)*STCWidth)
	return embedSTC(img, positions, payload, costs, flip)
}

// ExtractBlob reads a whole blob back from a carrier without any crypto (§7.9
// steps 3 and 5-8).
//
// blobSize must be the full blob length; the caller knows it from the decrypted
// header's body_len. The salt is read from S first (no key is needed), then
// posSeed drives the STC pool. The result is salt followed by payload, i.e. the
// original blob byte for byte.
func ExtractBlob(img *image.NRGBA, blobSize int, posSeed [32]byte) ([]byte, error) {
	channels := ChannelCount(img)
	if channels == 0 {
		return nil, ErrTooSmall
	}
	if blobSize < format.SaltSize {
		return nil, fmt.Errorf("%w: %d-byte blob is shorter than the %d-byte salt",
			ErrTooSmall, blobSize, format.SaltSize)
	}

	salt, err := ExtractSalt(img, format.SaltSize)
	if err != nil {
		return nil, err
	}

	payloadSize := blobSize - format.SaltSize
	positions := PoolPositions(channels, posSeed, payloadSize*STCWidth)
	payload, err := ExtractSTC(img, positions, payloadSize)
	if err != nil {
		return nil, err
	}

	return append(salt, payload...), nil
}

// VerifyBlob re-extracts the blob and compares it to want, returning ErrVerify
// on any difference (§7.8 step 14). The writer runs this on the saved file, so a
// single corrupted channel — including one the reader would silently accept —
// aborts the operation instead of shipping a broken carrier.
func VerifyBlob(img *image.NRGBA, want []byte, posSeed [32]byte) error {
	got, err := ExtractBlob(img, len(want), posSeed)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return ErrVerify
	}
	return nil
}
