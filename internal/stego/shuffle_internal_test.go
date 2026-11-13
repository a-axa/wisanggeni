package stego

import (
	"math/rand/v2"
	"testing"
)

// TestShuffleVariantsAgree pins the §7.1 requirement that the dense and map
// variants emit the same sequence. If they ever diverge, a carrier large enough
// to cross the DenseDivisor threshold would decode differently from a small one.
func TestShuffleVariantsAgree(t *testing.T) {
	seeds := [][32]byte{
		{},
		saltSeed,
		sha256Seed("variant-seed"),
	}

	for _, seed := range seeds {
		for _, n := range []uint64{1, 2, 3, 10, 192, 1000, 65536} {
			for _, k := range []int{1, 2, 7, 64, 1000} {
				if uint64(k) > n {
					continue
				}
				dense := shuffleDense(rand.NewChaCha8(seed), n, k)
				sparse := shuffleMap(rand.NewChaCha8(seed), n, k)

				if len(dense) != len(sparse) {
					t.Fatalf("n=%d k=%d: lengths %d and %d", n, k, len(dense), len(sparse))
				}
				for i := range dense {
					if dense[i] != sparse[i] {
						t.Fatalf("n=%d k=%d: index %d dense=%d map=%d", n, k, i, dense[i], sparse[i])
					}
				}
			}
		}
	}
}

func TestShuffleIsPermutationOfPrefix(t *testing.T) {
	const n = 4096
	const k = 64
	seed := sha256Seed("permutation")

	got := Shuffle(n, seed, k)
	if len(got) != k {
		t.Fatalf("len = %d, want %d", len(got), k)
	}

	seen := make(map[uint32]bool, k)
	for i, v := range got {
		if v >= n {
			t.Errorf("index %d = %d is out of range", i, v)
		}
		if seen[v] {
			t.Errorf("index %d = %d repeats", i, v)
		}
		seen[v] = true
	}
}

func TestShuffleEdgeCases(t *testing.T) {
	seed := sha256Seed("edge")

	if got := Shuffle(0, seed, 5); got != nil {
		t.Errorf("Shuffle(n=0) = %v, want nil", got)
	}
	if got := Shuffle(10, seed, 0); got != nil {
		t.Errorf("Shuffle(k=0) = %v, want nil", got)
	}
	if got := Shuffle(4, seed, 100); len(got) != 4 {
		t.Errorf("Shuffle(k>n) length = %d, want 4", len(got))
	}
}

// TestBoundedUniformity is a coarse bias check: a small modulus and many draws
// must not skew one value by more than a tenth of the expected count.
func TestBoundedUniformity(t *testing.T) {
	const (
		mod     = uint64(6)
		samples = 60_000
	)

	rng := rand.NewChaCha8(sha256Seed("uniformity"))
	counts := make([]int, mod)
	for i := 0; i < samples; i++ {
		v := bounded(rng, mod)
		if v >= mod {
			t.Fatalf("bounded returned %d for n=%d", v, mod)
		}
		counts[v]++
	}

	want := samples / int(mod)
	tolerance := want / 10
	for value, count := range counts {
		if count < want-tolerance || count > want+tolerance {
			t.Errorf("value %d drawn %d times, want %d +/- %d", value, count, want, tolerance)
		}
	}
}

func TestBoundedDegenerateModuli(t *testing.T) {
	rng := rand.NewChaCha8(sha256Seed("degenerate"))

	if got := bounded(rng, 0); got != 0 {
		t.Errorf("bounded(n=0) = %d, want 0", got)
	}
	for i := 0; i < 100; i++ {
		if got := bounded(rng, 1); got != 0 {
			t.Fatalf("bounded(n=1) = %d, want 0", got)
		}
	}
}
