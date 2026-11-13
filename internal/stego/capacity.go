package stego

import (
	"github.com/a-axa/wisanggeni/internal/format"
)

// Channels returns T = W × H × 3, the number of embeddable channels (§6.1).
func Channels(w, h int) int64 {
	if w <= 0 || h <= 0 {
		return 0
	}
	return int64(w) * int64(h) * 3
}

// CapacityRaw returns the largest blob the carrier can hold, in bytes (§6.1).
//
// The STC pool is T minus the 128 salt channels, and with rate h/w = 1/2 one
// message byte needs STCWidth pool channels, so the raw capacity is
// floor((T - 128) / STCWidth) bytes.
func CapacityRaw(w, h int) int64 {
	pool := Channels(w, h) - SaltChannels
	if pool <= 0 {
		return 0
	}
	blocks := pool / STCWidth
	return blocks * STCHeight / format.BitsPerByte
}

// Capacity returns the largest body_plain, i.e. the raw capacity minus the
// fixed container overhead. Pack uses this value directly as maxBytes (§6.1).
func Capacity(w, h int) int64 {
	return nonNegative(CapacityRaw(w, h) - format.BlobOverhead)
}

// SuggestedCapacity returns the largest body_plain at a 10% fill (§6.1).
func SuggestedCapacity(w, h int) int64 {
	return nonNegative(Channels(w, h)/80 - format.BlobOverhead)
}

// StrongCapacity returns the largest body_plain at a 5% fill (§6.1).
func StrongCapacity(w, h int) int64 {
	return nonNegative(Channels(w, h)/160 - format.BlobOverhead)
}

// FillRatio is the fraction of T consumed by a blob of blobBytes bytes (§6.1).
// It is defined on the blob, not on body_plain.
func FillRatio(blobBytes int64, w, h int) float64 {
	t := Channels(w, h)
	if t == 0 {
		return 0
	}
	return float64(format.BitsPerByte) * float64(blobBytes) / float64(t)
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
