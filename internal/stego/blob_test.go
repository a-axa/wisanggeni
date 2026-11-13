package stego_test

import (
	"bytes"
	"errors"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

func TestBlobRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 32))

	for _, dims := range [][2]int{{16, 16}, {64, 64}, {81, 41}} {
		w, h := dims[0], dims[1]
		limit := stego.CapacityRaw(w, h)

		sizes := []int64{format.SaltSize, format.SaltSize + 1, 2 * format.SaltSize, limit * 9 / 10, limit}
		for _, size := range sizes {
			if size < format.SaltSize || size > limit {
				continue
			}

			img := randomNRGBA(rng, w, h)
			blob := make([]byte, size)
			for i := range blob {
				blob[i] = byte(rng.IntN(256))
			}
			seed := testPosSeed(byte(size))

			costs := stego.CostMap(img)
			if err := stego.EmbedBlob(img, blob, seed, costs); err != nil {
				t.Fatalf("%dx%d size %d: EmbedBlob: %v", w, h, size, err)
			}

			got, err := stego.ExtractBlob(img, int(size), seed)
			if err != nil {
				t.Fatalf("%dx%d size %d: ExtractBlob: %v", w, h, size, err)
			}
			if !bytes.Equal(got, blob) {
				t.Fatalf("%dx%d size %d: round trip = %x, want %x", w, h, size, got, blob)
			}

			if err := stego.VerifyBlob(img, blob, seed); err != nil {
				t.Fatalf("%dx%d size %d: VerifyBlob: %v", w, h, size, err)
			}
		}
	}
}

func TestBlobVerifyCatchesOneBit(t *testing.T) {
	rng := rand.New(rand.NewPCG(41, 42))
	seed := testPosSeed(9)

	cases := []struct {
		name string
		pick func(img *image.NRGBA, blob []byte) uint32
	}{
		{
			name: "salt channel",
			pick: func(img *image.NRGBA, _ []byte) uint32 {
				return stego.SaltPositions(stego.ChannelCount(img))[0]
			},
		},
		{
			name: "pool channel",
			pick: func(img *image.NRGBA, blob []byte) uint32 {
				pool := stego.PoolPositions(
					stego.ChannelCount(img), seed, (len(blob)-format.SaltSize)*stego.STCWidth)
				return pool[0]
			},
		},
	}

	for _, tc := range cases {
		img := randomNRGBA(rng, 64, 64)
		blob := make([]byte, 200)
		for i := range blob {
			blob[i] = byte(rng.IntN(256))
		}

		costs := stego.CostMap(img)
		if err := stego.EmbedBlob(img, blob, seed, costs); err != nil {
			t.Fatalf("%s: EmbedBlob: %v", tc.name, err)
		}
		if err := stego.VerifyBlob(img, blob, seed); err != nil {
			t.Fatalf("%s: VerifyBlob before corruption: %v", tc.name, err)
		}

		// Flip one embedded bit; verification must notice.
		channel := tc.pick(img, blob)
		stego.SetLSB(img, channel, 1-stego.LSB(img, channel))

		if err := stego.VerifyBlob(img, blob, seed); !errors.Is(err, stego.ErrVerify) {
			t.Fatalf("%s: VerifyBlob after flip = %v, want ErrVerify", tc.name, err)
		}
	}
}

func TestBlobVerifyRejectsDifferentBlob(t *testing.T) {
	rng := rand.New(rand.NewPCG(51, 52))
	seed := testPosSeed(3)

	img := randomNRGBA(rng, 64, 64)
	blob := make([]byte, 200)
	for i := range blob {
		blob[i] = byte(rng.IntN(256))
	}

	costs := stego.CostMap(img)
	if err := stego.EmbedBlob(img, blob, seed, costs); err != nil {
		t.Fatalf("EmbedBlob: %v", err)
	}

	corrupted := append([]byte(nil), blob...)
	corrupted[len(corrupted)-1] ^= 0x80

	if err := stego.VerifyBlob(img, corrupted, seed); !errors.Is(err, stego.ErrVerify) {
		t.Fatalf("VerifyBlob against a different blob = %v, want ErrVerify", err)
	}
}

func TestBlobRejectsBadInput(t *testing.T) {
	img := solidNRGBA(16, 16, 128)
	costs := stego.CostMap(img)
	seed := testPosSeed(1)

	if err := stego.EmbedBlob(img, make([]byte, format.SaltSize-1), seed, costs); !errors.Is(err, stego.ErrTooSmall) {
		t.Errorf("EmbedBlob with a short blob = %v, want ErrTooSmall", err)
	}
	if _, err := stego.ExtractBlob(img, format.SaltSize-1, seed); !errors.Is(err, stego.ErrTooSmall) {
		t.Errorf("ExtractBlob with a short size = %v, want ErrTooSmall", err)
	}
	if err := stego.EmbedBlob(nil, make([]byte, 64), seed, nil); !errors.Is(err, stego.ErrTooSmall) {
		t.Errorf("EmbedBlob into a nil carrier = %v, want ErrTooSmall", err)
	}

	// A 16x16 carrier has a 640-channel pool, so at most 40 payload bytes:
	// 57 blob bytes need 41*16 = 656 > 640 channels.
	if err := stego.EmbedBlob(img, make([]byte, 57), seed, costs); !errors.Is(err, stego.ErrTooSmall) {
		t.Errorf("EmbedBlob into a carrier that is too small = %v, want ErrTooSmall", err)
	}
}
