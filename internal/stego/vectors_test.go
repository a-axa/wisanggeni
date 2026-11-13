package stego_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"image"
	"math"
	"math/bits"
	"math/rand/v2"
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// The vectors in this file are frozen by docs/FORMAT.md (§6.1). They must not
// be regenerated to make a failing implementation pass: a mismatch means the
// implementation drifted from format v1.

func TestBitOrder(t *testing.T) {
	// §6.1: byte 0xA5 -> bits 1,0,1,0,0,1,0,1 (MSB first).
	const sample = byte(0xA5)
	want := []byte{1, 0, 1, 0, 0, 1, 0, 1}

	for j, wantBit := range want {
		got := (sample & stego.BitMask(j)) / stego.BitMask(j)
		if got != wantBit {
			t.Errorf("bit %d of 0x%02X = %d, want %d", j, sample, got, wantBit)
		}
	}

	seen := make(map[byte]bool, format.BitsPerByte)
	for j := 0; j < format.BitsPerByte; j++ {
		mask := stego.BitMask(j)
		if seen[mask] {
			t.Fatalf("mask for bit %d repeats", j)
		}
		seen[mask] = true
		if want := byte(0x80 >> j); mask != want {
			t.Fatalf("mask for bit %d = 0x%02X, want 0x%02X", j, mask, want)
		}
	}
}

func TestChannelRGB(t *testing.T) {
	cases := []struct {
		channel   uint32
		pixel     uint32
		component uint8
	}{
		{0, 0, 0},
		{1, 0, 1},
		{2, 0, 2},
		{3, 1, 0},
		{191, 63, 2},
		{format.MaxChannelIndex, format.MaxChannelIndex / 3, format.MaxChannelIndex % 3},
	}
	for _, tc := range cases {
		pixel, component := stego.ChannelRGB(tc.channel)
		if pixel != tc.pixel || component != tc.component {
			t.Errorf("ChannelRGB(%d) = (%d,%d), want (%d,%d)",
				tc.channel, pixel, component, tc.pixel, tc.component)
		}
	}
}

func TestSaltPositionsGolden(t *testing.T) {
	// T = 192 comes from an 8x8 carrier, the smallest image used by the cost
	// map vector. Salt uses the dense shuffle because 128 > n/10.
	got := saltPositions(t, 192)

	first := []uint32{148, 65, 169, 39, 146, 140, 26, 187}
	for i, want := range first {
		if got[i] != want {
			t.Errorf("S[%d] = %d, want %d", i, got[i], want)
		}
	}

	const wantHash = "9373c9f0ea969158b29c86fd10a166056a7dfb993491519922fac43aa149ac06"
	if hash := hashUint32(got); hash != wantHash {
		t.Errorf("SHA-256 of S = %s, want %s", hash, wantHash)
	}
}

func TestCostMapGolden(t *testing.T) {
	const (
		wantHash = "9e1741de9274574aa7f575e40cb6b97cd550f61f0b38a4ea534c5b38e327a26a"
	)

	got := stego.CostMap(costVectorImage(8, 8))
	if len(got) != 8*8*3 {
		t.Fatalf("len(cost map) = %d, want %d", len(got), 8*8*3)
	}

	if hash := hashFloat64(got); hash != wantHash {
		t.Errorf("SHA-256 of cost map = %s, want %s", hash, wantHash)
	}

	first := []float64{
		0.023248605083694977,
		0.025999537785994915,
		0.034059945504087197,
		0.013199577613516367,
		0.015002000266702229,
		0.01971263360785001,
		0.0066646919431279615,
		0.0078561452513966484,
	}
	for i, want := range first {
		if got[i] != want {
			t.Errorf("cost[%d] = %.17g, want %.17g", i, got[i], want)
		}
	}
}

func TestCostMapFlatAndEdges(t *testing.T) {
	const flatCost = 1 / stego.CostEpsilon // S = 0 on a flat plane

	flat := solidNRGBA(8, 8, 128)
	for i, c := range stego.CostMap(flat) {
		if c != flatCost {
			t.Fatalf("flat cost[%d] = %.17g, want %.17g", i, c, flatCost)
		}
	}

	// 0 is an edge value: LSB matching only has one direction, so the cost is
	// multiplied by 4.
	zero := solidNRGBA(1, 1, 0)
	for i, c := range stego.CostMap(zero) {
		want := flatCost * stego.CostEdgeMultiplier
		if c != want {
			t.Fatalf("zero-edge cost[%d] = %.17g, want %.17g", i, c, want)
		}
	}
}

func TestCostMapTexturedIsCheaper(t *testing.T) {
	costs := stego.CostMap(costVectorImage(8, 8))
	if got, want := meanChannels(costs, 0), meanChannels(costs, 7); got <= want {
		t.Errorf("smooth column costs %.6f, dotted column costs %.6f; smooth must be dearer", got, want)
	}
}

// costVectorImage is the frozen 8x8 pattern of docs/FORMAT.md: x < 4 is flat
// 128, x >= 4 is the pseudo-texture (x*37 + y*17 + c*53 + x*y*97) mod 256.
func costVectorImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				v := byte(128)
				if x >= 4 {
					v = byte((x*37 + y*17 + c*53 + x*y*97) % 256)
				}
				img.Pix[i+c] = v
			}
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

func solidNRGBA(w, h int, value byte) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = value
		img.Pix[i+1] = value
		img.Pix[i+2] = value
		img.Pix[i+3] = 0xff
	}
	return img
}

func meanChannels(costs []float64, x int) float64 {
	const width = 8
	var sum float64
	for y := 0; y < 8; y++ {
		for c := 0; c < 3; c++ {
			sum += costs[(y*width+x)*3+c]
		}
	}
	return sum / 24
}

func saltPositions(t *testing.T, channels int64) []uint32 {
	t.Helper()

	seed := sha256.Sum256([]byte(format.LabelSaltPos))
	rng := rand.NewChaCha8(seed)

	n := uint64(channels)
	arr := make([]uint32, n)
	for i := range arr {
		arr[i] = uint32(i)
	}

	out := make([]uint32, 0, stego.SaltChannels)
	for i := uint64(0); i < uint64(stego.SaltChannels); i++ {
		j := i + bounded(rng, n-i)
		arr[i], arr[j] = arr[j], arr[i]
		out = append(out, arr[i])
	}
	return out
}

// bounded is the Lemire sampler of §7.1. It is duplicated here on purpose: the
// vector must be produced without trusting the implementation under test.
func bounded(rng *rand.ChaCha8, n uint64) uint64 {
	x := rng.Uint64()
	hi, lo := bits.Mul64(x, n)
	if lo < n {
		threshold := (-n) % n
		for lo < threshold {
			x = rng.Uint64()
			hi, lo = bits.Mul64(x, n)
		}
	}
	return hi
}

func hashUint32(values []uint32) string {
	h := sha256.New()
	var buf [4]byte
	for _, v := range values {
		binary.LittleEndian.PutUint32(buf[:], v)
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFloat64(values []float64) string {
	h := sha256.New()
	var buf [8]byte
	for _, v := range values {
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
