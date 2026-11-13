package stego_test

import (
	"testing"

	"github.com/a-axa/wisanggeni/internal/stego"
)

// The 12 MP figure the format's memory rule is written against (§7.1): a
// 4000x3000 carrier has T = 36,000,000 channels.
const (
	benchChannels = 36_000_000
	// Typical payload: SuggestedCapacity(4000, 3000) is 310,928 body bytes, so
	// the blob is 311,040 bytes and STC writes everything but the 16-byte salt,
	// m = 8 * (311_040 - 16) bits.
	benchPayloadBits = 8 * (311_040 - 16)
)

// BenchmarkPoolPositions12MP measures the peak working set of P' for a typical
// payload on a 12 MP carrier. Run with:
//
//	go test -run '^$' -bench BenchmarkPoolPositions12MP -benchmem ./internal/stego
func BenchmarkPoolPositions12MP(b *testing.B) {
	seed := testPosSeed(0x42)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		positions := stego.PoolPositions(benchChannels, seed, benchPayloadBits)
		if len(positions) != benchPayloadBits {
			b.Fatalf("len = %d, want %d", len(positions), benchPayloadBits)
		}
	}
}

// BenchmarkPoolPositions12MPDense measures the worst case: a payload just past
// the DenseDivisor threshold, which is where the dense variant is chosen and
// allocates a full 4*N-byte index array.
func BenchmarkPoolPositions12MPDense(b *testing.B) {
	seed := testPosSeed(0x42)
	// k = n/10 + 1 with n = 36,000,000 - 128.
	k := (benchChannels-stego.SaltChannels)/stego.DenseDivisor + 1

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		positions := stego.PoolPositions(benchChannels, seed, k)
		if len(positions) != k {
			b.Fatalf("len = %d, want %d", len(positions), k)
		}
	}
}

func BenchmarkPoolPositionsSmall(b *testing.B) {
	seed := testPosSeed(0x42)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := stego.PoolPositions(192, seed, 32); len(got) != 32 {
			b.Fatalf("len = %d, want 32", len(got))
		}
	}
}
