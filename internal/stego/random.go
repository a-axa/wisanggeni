package stego

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"math/bits"
	"math/rand/v2"

	"github.com/a-axa/wisanggeni/internal/format"
)

// bounded returns a uniform value in [0, n) with Lemire's multiply-and-reject
// method (§7.1). It only ever calls Uint64(), because the frozen stream is the
// raw ChaCha8 sequence; rand.Rand's own bounded helpers consume it differently
// and would desynchronise every golden vector.
//
// n == 0 has no valid answer and is only reachable from a zero-channel carrier,
// which the callers reject first; returning 0 keeps the function total.
func bounded(rng *rand.ChaCha8, n uint64) uint64 {
	if n == 0 {
		return 0
	}
	x := rng.Uint64()
	hi, lo := bits.Mul64(x, n)
	if lo < n {
		// threshold is (2^64 - n) % n, the count of low products to reject.
		threshold := (-n) % n
		for lo < threshold {
			x = rng.Uint64()
			hi, lo = bits.Mul64(x, n)
		}
	}
	return hi
}

// Flip reports the direction LSB matching takes when a value's lowest bit must
// change (§7.6): true steps the value up, false steps it down.
type Flip func() bool

// NewFlip returns a Flip driven by a fresh crypto/rand seed. It is for direct
// users of EmbedSalt and EmbedSTC, which have no container to derive from.
func NewFlip() Flip {
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		// crypto/rand does not fail on supported platforms; the fallback only
		// makes the function total.
		return func() bool { return true }
	}
	return streamFlip(seed)
}

// blobFlip derives the direction stream from the container bytes. The blob
// begins with the salt and the two AEAD nonces, so the stream is fresh for every
// real run, and an embed becomes a pure function of (cover, blob, pos_seed,
// costs): two writers handed the same container place the same pixels. That is
// what lets the CLI and the TUI be compared byte for byte.
//
// cargo-culting a fresh crypto/rand read per flipped channel would have cost a
// syscall per bit and made the output unreproducible.
func blobFlip(blob []byte) Flip {
	h := sha256.New()
	h.Write([]byte(format.LabelFlip))
	h.Write(blob)
	var seed [32]byte
	copy(seed[:], h.Sum(nil))
	return streamFlip(seed)
}

// streamFlip returns a Flip that consumes a ChaCha8 stream one bit at a time.
func streamFlip(seed [32]byte) Flip {
	rng := rand.NewChaCha8(seed)
	var word uint64
	left := 0
	return func() bool {
		if left == 0 {
			word = rng.Uint64()
			left = 64
		}
		up := word&1 == 1
		word >>= 1
		left--
		return up
	}
}
