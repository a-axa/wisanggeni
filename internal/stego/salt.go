package stego

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
)

// ErrTooSmall reports a carrier, or a position list, with fewer channels than
// an operation needs.
var ErrTooSmall = errors.New("stego: too few channels")

// ErrBadPosition reports a channel index outside the image.
var ErrBadPosition = errors.New("stego: channel position outside the image")

// sha256Seed returns the 32-byte ChaCha8 seed derived from a fixed label.
func sha256Seed(label string) [32]byte {
	return sha256.Sum256([]byte(label))
}

// SaltPositions returns S, the SaltChannels public salt positions (§7.2).
//
// S is the first SaltChannels elements of a shuffle of [0, T) seeded by
// SHA-256(LabelSaltPos), so it is the same for every carrier of a given size and
// needs no key: reveal reads the salt before it can derive one. If the carrier
// has fewer than SaltChannels channels the result is shorter than SaltChannels,
// and EmbedSalt/ExtractSalt reject it.
func SaltPositions(channels uint32) []uint32 {
	return Shuffle(uint64(channels), saltSeed, SaltChannels)
}

// EmbedSalt writes the salt with LSB matching at S (§7.2, §7.6).
func EmbedSalt(img *image.NRGBA, salt []byte) error {
	return EmbedBytes(img, SaltPositions(ChannelCount(img)), salt)
}

// ExtractSalt reads size bytes from S. It does not need any key.
func ExtractSalt(img *image.NRGBA, size int) ([]byte, error) {
	return ExtractBytes(img, SaltPositions(ChannelCount(img)), size)
}

// EmbedBits writes one bit per position, MSB first, using LSB matching (§7.6).
func EmbedBits(img *image.NRGBA, positions []uint32, bits []byte) error {
	return embedBits(img, positions, bits, randomFlip)
}

// ExtractBits reads one bit from each position, up to n bits.
func ExtractBits(img *image.NRGBA, positions []uint32, n int) ([]byte, error) {
	return extractBits(img, positions, n)
}

// EmbedBytes writes data MSB-first into the channels named by positions.
func EmbedBytes(img *image.NRGBA, positions []uint32, data []byte) error {
	return embedBits(img, positions, BytesToBits(data), randomFlip)
}

// ExtractBytes reads size bytes from the channels named by positions.
func ExtractBytes(img *image.NRGBA, positions []uint32, size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: negative size %d", ErrTooSmall, size)
	}
	bits, err := extractBits(img, positions, size*8)
	if err != nil {
		return nil, err
	}
	return BitsToBytes(bits), nil
}

// embedBits is the single write path; flip decides which way a value moves when
// its LSB must change, and is injectable so tests can pin the resulting pixels.
func embedBits(img *image.NRGBA, positions []uint32, bits []byte, flip func() bool) error {
	channels := ChannelCount(img)
	if channels == 0 {
		return ErrTooSmall
	}
	if len(positions) < len(bits) {
		return fmt.Errorf("%w: %d positions for %d bits", ErrTooSmall, len(positions), len(bits))
	}

	for i, bit := range bits {
		c := positions[i]
		if c >= channels {
			return fmt.Errorf("%w: channel %d of %d", ErrBadPosition, c, channels)
		}
		off := channelOffset(img, c)
		img.Pix[off] = matchLSB(img.Pix[off], bit&1, flip())
	}
	return nil
}

func extractBits(img *image.NRGBA, positions []uint32, n int) ([]byte, error) {
	channels := ChannelCount(img)
	if channels == 0 {
		return nil, ErrTooSmall
	}
	if n < 0 {
		return nil, fmt.Errorf("%w: negative bit count %d", ErrTooSmall, n)
	}
	if len(positions) < n {
		return nil, fmt.Errorf("%w: %d positions for %d bits", ErrTooSmall, len(positions), n)
	}

	out := make([]byte, n)
	for i := 0; i < n; i++ {
		c := positions[i]
		if c >= channels {
			return nil, fmt.Errorf("%w: channel %d of %d", ErrBadPosition, c, channels)
		}
		out[i] = LSB(img, c)
	}
	return out, nil
}

// matchLSB adjusts a value so its lowest bit equals bit, following §7.6: leave
// it alone when it already matches, otherwise step by one in the given
// direction, except at the ends where only one direction exists.
func matchLSB(v, bit byte, up bool) byte {
	if v&1 == bit&1 {
		return v
	}
	switch v {
	case 0:
		return 1
	case 255:
		return 254
	}
	if up {
		return v + 1
	}
	return v - 1
}
