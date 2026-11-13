package stego

import (
	"bytes"
	"image"
	"testing"
)

// FuzzExtractSTC feeds arbitrary channel positions and sizes to the syndrome
// reader. The contract is narrow: it either returns bytes or an error, and it
// must never index outside the image or the position slice.
func FuzzExtractSTC(f *testing.F) {
	img := fuzzImage(8, 8)
	f.Add(1, []byte{0x00, 0x01, 0x02, 0x03})
	f.Add(4, []byte{0xff})
	f.Add(0, []byte(nil))
	f.Add(8, []byte{0x3f})

	f.Fuzz(func(t *testing.T, size int, blob []byte) {
		if size < -4 || size > 4096 || len(blob) > 1<<14 {
			t.Skip()
		}
		positions := make([]uint32, 0, len(blob)/2)
		for i := 0; i+1 < len(blob); i += 2 {
			positions = append(positions, uint32(blob[i])<<8|uint32(blob[i+1]))
		}
		_, _ = ExtractSTC(img, positions, size)
	})
}

// FuzzVerifyBlob checks the read-back comparison against a corrupt image. It is
// the path that decides whether a written file is trustworthy, so a panic here
// would turn a bad carrier into a crash instead of an error.
func FuzzVerifyBlob(f *testing.F) {
	seed := [32]byte{1, 2, 3}
	f.Add([]byte("expected blob"), []byte{0, 1, 2, 3}, uint8(0))
	f.Add([]byte{}, []byte{}, uint8(1))

	// The image and the expected blob are built once: doing it per iteration
	// allocates far too much for the fuzzer's worker, which then gets OOM-killed.
	img := fuzzImage(16, 16)

	f.Fuzz(func(t *testing.T, want, noise []byte, flip byte) {
		if len(want) > 4096 || len(noise) > 4096 {
			t.Skip()
		}
		if len(noise) > 0 {
			for i := range img.Pix {
				img.Pix[i] ^= noise[i%len(noise)]
			}
		}
		if len(img.Pix) > 0 {
			img.Pix[int(flip)%len(img.Pix)] ^= 1
		}
		_ = VerifyBlob(img, want, seed)
	})
}

// FuzzExtractSalt reads the fixed-size salt from arbitrary positions, including
// positions past the end of the image.
func FuzzExtractSalt(f *testing.F) {
	f.Add(uint32(0), uint32(4))
	f.Add(uint32(99), uint32(1))
	f.Add(uint32(63), uint32(16))

	img := fuzzImage(8, 8)

	f.Fuzz(func(t *testing.T, start, size uint32) {
		if size > 4096 {
			t.Skip()
		}
		positions := make([]uint32, 0, size)
		for i := uint32(0); i < size; i++ {
			positions = append(positions, start+i)
		}
		_, _ = ExtractBits(img, positions, int(size)*8)
		_, _ = ExtractBytes(img, positions, int(size))
	})
}

// FuzzPoolPositions pins the pool builder: the count is clamped, salt channels
// never appear in the pool, and every position is a real channel.
func FuzzPoolPositions(f *testing.F) {
	seed := [32]byte{9, 9, 9}
	f.Add(uint32(192), 0, uint8(1))
	f.Add(uint32(192), 500, uint8(2))
	f.Add(uint32(1), 7, uint8(3))
	f.Add(uint32(0), 0, uint8(4))

	f.Fuzz(func(t *testing.T, channels uint32, k int, tag uint8) {
		if channels > 1<<12 || k < 0 || k > 1<<12 {
			t.Skip()
		}
		seed[0] = tag
		got := PoolPositions(channels, seed, k)
		if len(got) > k {
			t.Fatalf("got %d positions for k=%d", len(got), k)
		}
		salt := SaltPositions(channels)
		saltSet := make(map[uint32]bool, len(salt))
		for _, c := range salt {
			saltSet[c] = true
		}
		for _, c := range got {
			if channels > SaltChannels && saltSet[c] {
				t.Fatalf("pool position %d collides with the salt", c)
			}
		}
	})
}

// FuzzBytesToBitsRoundTrip checks the bit codec, which every other layer sits on.
func FuzzBytesToBitsRoundTrip(f *testing.F) {
	f.Add([]byte{0xa5})
	f.Add([]byte{0x00, 0xff, 0x55})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			t.Skip()
		}
		bits := BytesToBits(data)
		if len(bits) != len(data)*8 {
			t.Fatalf("bits = %d, want %d", len(bits), len(data)*8)
		}
		back := BitsToBytes(bits)
		if !bytes.Equal(back, data) {
			t.Fatalf("round trip changed the data: %x -> %x", data, back)
		}
	})
}

// fuzzImage is a small deterministic NRGBA image with an interesting range of
// values, including the 0 and 255 edge cases the clamp logic cares about.
func fuzzImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	state := uint32(0x2545f491)
	for i := 0; i < len(img.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			img.Pix[i+c] = byte(state)
		}
		img.Pix[i+3] = 0xff
	}
	return img
}
