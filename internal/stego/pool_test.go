package stego_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"testing"

	"github.com/a-axa/wisanggeni/internal/stego"
)

func TestPoolPositionsExcludeSaltAndCoverPool(t *testing.T) {
	const channels = 192 // 8x8 carrier
	salt := stego.SaltPositions(channels)
	saltSet := make(map[uint32]bool, len(salt))
	for _, s := range salt {
		saltSet[s] = true
	}

	pool := stego.PoolPositions(channels, testPosSeed(1), int(channels)-len(salt))
	if len(pool) != int(channels)-len(salt) {
		t.Fatalf("len(P') = %d, want %d", len(pool), int(channels)-len(salt))
	}

	seen := make(map[uint32]bool, len(pool))
	for i, c := range pool {
		if c >= channels {
			t.Errorf("P'[%d] = %d is out of range", i, c)
		}
		if saltSet[c] {
			t.Errorf("P'[%d] = %d is a salt position", i, c)
		}
		if seen[c] {
			t.Errorf("P'[%d] = %d repeats", i, c)
		}
		seen[c] = true
	}

	// A full pool is a permutation of every non-salt channel.
	var missing []uint32
	for c := uint32(0); c < channels; c++ {
		if !saltSet[c] && !seen[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) != 0 {
		t.Errorf("channels missing from P': %v", missing)
	}
}

func TestPoolPositionsPrefixStable(t *testing.T) {
	const channels = 4096
	n := int(channels) - stego.SaltChannels
	seed := testPosSeed(7)

	full := stego.PoolPositions(channels, seed, n)
	for _, k := range []int{1, 2, 5, 32, 100} {
		prefix := stego.PoolPositions(channels, seed, k)
		if !bytes.Equal(uint32Bytes(prefix), uint32Bytes(full[:k])) {
			t.Errorf("prefix of length %d changed when k changed", k)
		}
	}
}

func TestPoolPositionsDeterministicAndKeyed(t *testing.T) {
	const channels = 4096

	a := stego.PoolPositions(channels, testPosSeed(1), 64)
	b := stego.PoolPositions(channels, testPosSeed(1), 64)
	if !bytes.Equal(uint32Bytes(a), uint32Bytes(b)) {
		t.Fatal("P' is not deterministic for the same seed")
	}

	c := stego.PoolPositions(channels, testPosSeed(2), 64)
	if bytes.Equal(uint32Bytes(a), uint32Bytes(c)) {
		t.Fatal("P' ignores posSeed")
	}
}

func TestPoolPositionsTinyCarrier(t *testing.T) {
	// T = 3 is fully consumed by salt (S is clamped to the channel count).
	if got := stego.PoolPositions(3, testPosSeed(1), 1); got != nil {
		t.Fatalf("PoolPositions(3) = %v, want nil", got)
	}
	if got := stego.PoolPositions(192, testPosSeed(1), 0); got != nil {
		t.Fatalf("PoolPositions(k=0) = %v, want nil", got)
	}
}

func TestPoolPositionsClampToPool(t *testing.T) {
	const channels = 200 // pool = 72
	got := stego.PoolPositions(channels, testPosSeed(3), 10_000)
	if len(got) != channels-stego.SaltChannels {
		t.Fatalf("len = %d, want %d", len(got), channels-stego.SaltChannels)
	}

	sorted := append([]uint32(nil), got...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Fatalf("duplicate channel %d in a clamped full pool", sorted[i])
		}
	}
}

func TestPoolPositionsGolden(t *testing.T) {
	// Frozen regression vector: a fixed key must keep producing this prefix.
	// It lives in the test rather than docs/FORMAT.md because the shuffle's
	// dense/map split may still change before v1.0.0.
	got := stego.PoolPositions(192, testPosSeed(0xA5), 32)

	first := []uint32{
		141, 154, 44, 176, 128, 112, 188, 64,
		8, 85, 53, 67, 70, 76, 13, 105,
		164, 161, 12, 0, 61, 132, 173, 87,
		96, 81, 92, 36, 16, 155, 144, 99,
	}
	for i, want := range first {
		if got[i] != want {
			t.Errorf("P'[%d] = %d, want %d", i, got[i], want)
		}
	}

	const wantHash = "d8a951193bd06ce330f03372caafe58a06fce887922675b175fa947c26e14f7d"
	if hash := hashUint32Values(got); hash != wantHash {
		t.Errorf("SHA-256 of P' prefix = %s, want %s", hash, wantHash)
	}
}

// testPosSeed builds a recognisable 32-byte seed for the tests.
func testPosSeed(tag byte) [32]byte {
	var seed [32]byte
	for i := range seed {
		seed[i] = tag + byte(i*7)
	}
	return seed
}

func uint32Bytes(values []uint32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return out
}

func hashUint32Values(values []uint32) string {
	sum := sha256.Sum256(uint32Bytes(values))
	return hex.EncodeToString(sum[:])
}
