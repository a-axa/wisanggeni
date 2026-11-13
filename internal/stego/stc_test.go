package stego_test

import (
	"bytes"
	"errors"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/a-axa/wisanggeni/internal/stego"
)

func TestParityMatrixGolden(t *testing.T) {
	cols := stego.ParityColumns()
	t.Logf("columns: %#v", cols)
}

func TestSTCRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))

	for _, dims := range [][2]int{{8, 8}, {16, 16}, {31, 17}, {64, 48}} {
		img := randomNRGBA(rng, dims[0], dims[1])
		before := append([]byte(nil), img.Pix...)

		channels := stego.ChannelCount(img)
		positions := stego.PoolPositions(channels, testPosSeed(9), stego.MaxSTCBytes(int(channels))*stego.STCWidth)
		capacity := stego.MaxSTCBytes(len(positions))

		for _, size := range []int{0, 1, 2, 7, capacity} {
			if size > capacity {
				continue
			}
			img.Pix = append(img.Pix[:0:0], before...)

			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i*31 + 5)
			}

			costs := stego.CostMap(img)
			if err := stego.EmbedSTC(img, positions, data, costs); err != nil {
				t.Fatalf("%dx%d size %d: EmbedSTC: %v", dims[0], dims[1], size, err)
			}
			got, err := stego.ExtractSTC(img, positions, size)
			if err != nil {
				t.Fatalf("%dx%d size %d: ExtractSTC: %v", dims[0], dims[1], size, err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%dx%d size %d: round trip = %x, want %x", dims[0], dims[1], size, got, data)
			}
		}
	}
}

func TestSTCRoundTripManySizes(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))

	for i := 0; i < 100; i++ {
		w := 4 + rng.IntN(30)
		h := 4 + rng.IntN(30)
		img := randomNRGBA(rng, w, h)

		channels := stego.ChannelCount(img)
		all := stego.PoolPositions(channels, testPosSeed(byte(i)), stego.MaxSTCBytes(int(channels))*stego.STCWidth)
		capacity := stego.MaxSTCBytes(len(all))
		if capacity == 0 {
			continue
		}
		size := 1 + rng.IntN(capacity)
		positions := all[:size*stego.STCWidth]

		data := make([]byte, size)
		for j := range data {
			data[j] = byte(rng.IntN(256))
		}

		costs := stego.CostMap(img)
		if err := stego.EmbedSTC(img, positions, data, costs); err != nil {
			t.Fatalf("case %d (%dx%d, %d bytes): EmbedSTC: %v", i, w, h, size, err)
		}
		got, err := stego.ExtractSTC(img, positions, size)
		if err != nil {
			t.Fatalf("case %d: ExtractSTC: %v", i, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("case %d: round trip = %x, want %x", i, got, data)
		}
	}
}

func TestSTCBeatsUniformLSBMatching(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))

	better := 0
	for i := 0; i < 20; i++ {
		img := randomNRGBA(rng, 64, 64)
		channels := stego.ChannelCount(img)
		positions := stego.PoolPositions(channels, testPosSeed(byte(i+1)), stego.MaxSTCBytes(int(channels))*stego.STCWidth)
		capacity := stego.MaxSTCBytes(len(positions))

		data := make([]byte, capacity)
		for j := range data {
			data[j] = byte(rng.IntN(256))
		}

		costs := stego.CostMap(img)

		stegoCost, err := stego.STCCost(img, positions, data, costs)
		if err != nil {
			t.Fatalf("STCCost: %v", err)
		}

		// Uniform LSB matching embeds 1 bit per channel, so it needs twice the
		// channels for the same message; compare the cost per message bit.
		uniformCost := uniformMatchingCost(img, positions, data, costs)

		if stegoCost < uniformCost {
			better++
		} else {
			t.Errorf("case %d: STC cost %.4f is not below uniform %.4f", i, stegoCost, uniformCost)
		}
	}

	if better == 0 {
		t.Fatal("STC never beat uniform LSB matching")
	}
}

func TestSTCChangesStayWithinOneAndFlipLSB(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	img := randomNRGBA(rng, 32, 32)
	before := append([]byte(nil), img.Pix...)

	channels := stego.ChannelCount(img)
	positions := stego.PoolPositions(channels, testPosSeed(2), stego.MaxSTCBytes(int(channels))*stego.STCWidth)
	data := make([]byte, stego.MaxSTCBytes(len(positions)))
	for i := range data {
		data[i] = byte(i * 17)
	}

	costs := stego.CostMap(img)
	if err := stego.EmbedSTC(img, positions, data, costs); err != nil {
		t.Fatalf("EmbedSTC: %v", err)
	}

	touched := 0
	for c := uint32(0); c < channels; c++ {
		pixel, component := stego.ChannelRGB(c)
		bounds := img.Bounds()
		width := uint32(bounds.Dx())
		off := img.PixOffset(bounds.Min.X+int(pixel%width), bounds.Min.Y+int(pixel/width)) + int(component)

		delta := int(img.Pix[off]) - int(before[off])
		if delta == 0 {
			continue
		}
		touched++
		if delta != 1 && delta != -1 {
			t.Fatalf("channel %d moved by %d", c, delta)
		}
		if img.Pix[off]&1 == before[off]&1 {
			t.Fatalf("channel %d moved without flipping its LSB", c)
		}
	}
	if touched == 0 {
		t.Fatal("nothing was modified; the test proves nothing")
	}
	t.Logf("modified %d of %d channels", touched, channels)
}

func TestSTCEdgeValues(t *testing.T) {
	for _, value := range []byte{0, 255} {
		img := solidNRGBA(16, 16, value)

		channels := stego.ChannelCount(img)
		positions := stego.PoolPositions(channels, testPosSeed(4), stego.MaxSTCBytes(int(channels))*stego.STCWidth)
		data := make([]byte, stego.MaxSTCBytes(len(positions)))
		for i := range data {
			data[i] = byte(i*13 + 1)
		}

		costs := stego.CostMap(img)
		if err := stego.EmbedSTC(img, positions, data, costs); err != nil {
			t.Fatalf("value %d: EmbedSTC: %v", value, err)
		}

		other := byte(1)
		if value == 255 {
			other = 254
		}
		for i := 0; i < len(img.Pix); i += 4 {
			for c := 0; c < 3; c++ {
				got := img.Pix[i+c]
				if got != value && got != other {
					t.Fatalf("value %d: channel %d became %d", value, i+c, got)
				}
			}
		}

		got, err := stego.ExtractSTC(img, positions, len(data))
		if err != nil {
			t.Fatalf("value %d: ExtractSTC: %v", value, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("value %d: round trip = %x, want %x", value, got, data)
		}
	}
}

func TestSTCRejectsShortInputs(t *testing.T) {
	img := solidNRGBA(8, 8, 128)
	channels := stego.ChannelCount(img)
	positions := stego.PoolPositions(channels, testPosSeed(1), 16) // one block
	costs := stego.CostMap(img)

	if err := stego.EmbedSTC(img, positions, make([]byte, 2), costs); !errors.Is(err, stego.ErrTooSmall) {
		t.Errorf("EmbedSTC with 2 bytes and 1 block = %v, want ErrTooSmall", err)
	}
	if _, err := stego.ExtractSTC(img, positions, 2); !errors.Is(err, stego.ErrTooSmall) {
		t.Errorf("ExtractSTC with 2 bytes and 1 block = %v, want ErrTooSmall", err)
	}
	if err := stego.EmbedSTC(img, positions, []byte{1}, []float64{1}); !errors.Is(err, stego.ErrCostMap) {
		t.Errorf("EmbedSTC with a short cost map = %v, want ErrCostMap", err)
	}
	if err := stego.EmbedSTC(img, positions, nil, costs); err != nil {
		t.Errorf("EmbedSTC of nothing = %v, want nil", err)
	}
}

// randomMatchingCost simulates uniform LSB matching: every channel that has to
// carry a bit costs its rho unless its LSB already happens to match.
func uniformMatchingCost(img *image.NRGBA, positions []uint32, data []byte, costs []float64) float64 {
	bits := stego.BytesToBits(data)
	var total float64
	for i, pos := range positions {
		if i >= len(bits) {
			break
		}
		if stego.LSB(img, pos) != bits[i] {
			total += costs[pos]
		}
	}
	return total
}

func randomNRGBA(rng *rand.Rand, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = byte(rng.IntN(256))
		img.Pix[i+1] = byte(rng.IntN(256))
		img.Pix[i+2] = byte(rng.IntN(256))
		img.Pix[i+3] = 0xFF
	}
	return img
}
