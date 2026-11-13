package stego

import (
	cryptorand "crypto/rand"
	"math/bits"
	"math/rand/v2"
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

// randomFlip picks the direction LSB matching uses when it must move a value
// (§7.6). crypto/rand.Read does not fail on supported platforms; the fallback
// only makes the function total, never a normal path.
func randomFlip() bool {
	var b [1]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return true
	}
	return b[0]&1 == 1
}
