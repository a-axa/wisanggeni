package stego

import (
	"math/rand/v2"

	"github.com/a-axa/wisanggeni/internal/format"
)

// Shuffle yields the first k elements of a Fisher-Yates permutation of [0, n),
// driven by seed (§7.1). It is the single permutation primitive behind both the
// public salt positions and the keyed STC pool, so any change to it changes the
// format.
//
// Two variants implement the same permutation:
//
//   - dense, when k > n/DenseDivisor: a full slice of n uint32 (about 4n bytes);
//   - map, otherwise: a map that only ever holds the k touched slots.
//
// The choice is a memory heuristic and may be revised before v1.0.0, as long as
// the emitted sequence does not change; TestShuffleVariantsAgree pins that.
//
// n must fit the uint32 positions the format stores, so n <= 1<<32. A larger n
// would truncate silently, which is why callers derive n from a guarded carrier.
func Shuffle(n uint64, seed [32]byte, k int) []uint32 {
	if k <= 0 || n == 0 {
		return nil
	}
	if uint64(k) > n {
		k = int(n)
	}

	rng := rand.NewChaCha8(seed)
	if uint64(k) > n/uint64(DenseDivisor) {
		return shuffleDense(rng, n, k)
	}
	return shuffleMap(rng, n, k)
}

func shuffleDense(rng *rand.ChaCha8, n uint64, k int) []uint32 {
	arr := make([]uint32, n)
	for i := range arr {
		arr[i] = uint32(i)
	}

	out := make([]uint32, 0, k)
	for i := 0; i < k; i++ {
		j := uint64(i) + bounded(rng, n-uint64(i))
		arr[i], arr[j] = arr[j], arr[i]
		out = append(out, arr[i])
	}
	return out
}

func shuffleMap(rng *rand.ChaCha8, n uint64, k int) []uint32 {
	swapped := make(map[uint64]uint32, k)
	out := make([]uint32, 0, k)
	for i := 0; i < k; i++ {
		j := uint64(i) + bounded(rng, n-uint64(i))

		vi, ok := swapped[uint64(i)]
		if !ok {
			vi = uint32(i)
		}
		vj, ok := swapped[j]
		if !ok {
			vj = uint32(j)
		}

		// Slot i is never read again, so writing vi to j is enough; this is
		// what keeps the map at k entries instead of n.
		swapped[j] = vi
		out = append(out, vj)
	}
	return out
}

// saltSeed is the fixed, public seed for the salt positions (§7.2). The salt
// randomness lives in the salt bytes, not in where they are written.
var saltSeed = sha256Seed(format.LabelSaltPos)
