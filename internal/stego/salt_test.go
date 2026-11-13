package stego_test

import (
	"bytes"
	"errors"
	"image"
	"reflect"
	"testing"

	"github.com/a-axa/wisanggeni/internal/stego"
)

func TestBytesToBitsMSBFirst(t *testing.T) {
	// §6.1 freezes 0xA5 as 1,0,1,0,0,1,0,1.
	got := stego.BytesToBits([]byte{0xA5})
	want := []byte{1, 0, 1, 0, 0, 1, 0, 1}
	if !bytes.Equal(got, want) {
		t.Fatalf("BytesToBits(0xA5) = %v, want %v", got, want)
	}

	for _, data := range [][]byte{nil, {}, {0x00}, {0xFF}, {0xA5, 0x00, 0xFF}} {
		if p := stego.BitsToBytes(stego.BytesToBits(data)); !bytes.Equal(p, data) {
			t.Errorf("round trip %x = %x", data, p)
		}
	}
}

func TestChannelCount(t *testing.T) {
	if got := stego.ChannelCount(nil); got != 0 {
		t.Errorf("ChannelCount(nil) = %d, want 0", got)
	}
	if got := stego.ChannelCount(solidNRGBA(8, 8, 0)); got != 192 {
		t.Errorf("ChannelCount(8x8) = %d, want 192", got)
	}
	if got := stego.ChannelCount(solidNRGBA(1, 1, 0)); got != 3 {
		t.Errorf("ChannelCount(1x1) = %d, want 3", got)
	}
}

func TestLSBAndSetLSB(t *testing.T) {
	img := solidNRGBA(2, 2, 128) // all even
	stego.SetLSB(img, 5, 1)
	if got := stego.LSB(img, 5); got != 1 {
		t.Fatalf("LSB after SetLSB = %d, want 1", got)
	}
	stego.SetLSB(img, 5, 0)
	if got := stego.LSB(img, 5); got != 0 {
		t.Fatalf("LSB after clearing = %d, want 0", got)
	}
	// Alpha is never a channel and must stay opaque.
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0xFF {
			t.Fatalf("alpha at %d = %d, want 255", i, img.Pix[i])
		}
	}
}

func TestSaltPositionsInRangeUniqueDeterministic(t *testing.T) {
	for _, channels := range []uint32{128, 129, 192, 4096, 1_000_000} {
		pos := stego.SaltPositions(channels)
		if len(pos) != stego.SaltChannels {
			t.Fatalf("len(S) for T=%d = %d, want %d", channels, len(pos), stego.SaltChannels)
		}

		seen := make(map[uint32]bool, len(pos))
		for i, p := range pos {
			if p >= channels {
				t.Errorf("T=%d: S[%d] = %d is out of range", channels, i, p)
			}
			if seen[p] {
				t.Errorf("T=%d: S[%d] = %d repeats", channels, i, p)
			}
			seen[p] = true
		}

		if again := stego.SaltPositions(channels); !reflect.DeepEqual(pos, again) {
			t.Errorf("T=%d: SaltPositions is not deterministic", channels)
		}
	}
}

func TestSaltRoundTrip(t *testing.T) {
	// 0 bytes (no-op), 1 byte, and exactly the salt capacity of 128 channels.
	for _, size := range []int{0, 1, stego.SaltChannels / 8} {
		img := costVectorImage(8, 8)
		salt := make([]byte, size)
		for i := range salt {
			salt[i] = byte(i*37 + 11)
		}

		if err := stego.EmbedSalt(img, salt); err != nil {
			t.Fatalf("size %d: EmbedSalt: %v", size, err)
		}
		got, err := stego.ExtractSalt(img, size)
		if err != nil {
			t.Fatalf("size %d: ExtractSalt: %v", size, err)
		}
		if !bytes.Equal(got, salt) {
			t.Fatalf("size %d: round trip = %x, want %x", size, got, salt)
		}
	}
}

func TestSaltOnlyMovesLSB(t *testing.T) {
	img := costVectorImage(8, 8)
	before := append([]byte(nil), img.Pix...)

	salt := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if err := stego.EmbedSalt(img, salt); err != nil {
		t.Fatalf("EmbedSalt: %v", err)
	}

	for channel, pos := range stego.SaltPositions(stego.ChannelCount(img)) {
		pixel, component := stego.ChannelRGB(pos)
		off := img.PixOffset(int(pixel%8), int(pixel/8)) + int(component)
		delta := int(img.Pix[off]) - int(before[off])
		if delta < -1 || delta > 1 {
			t.Fatalf("channel %d moved by %d", channel, delta)
		}
	}

	got, err := stego.ExtractSalt(img, len(salt))
	if err != nil {
		t.Fatalf("ExtractSalt: %v", err)
	}
	if !bytes.Equal(got, salt) {
		t.Fatalf("extracted %x, want %x", got, salt)
	}
}

func TestSaltNeverWrapsAtValueEdges(t *testing.T) {
	for _, value := range []byte{0, 255} {
		img := solidNRGBA(8, 8, value)
		if err := stego.EmbedSalt(img, []byte{0xFF, 0x00, 0xFF, 0x00}); err != nil {
			t.Fatalf("value %d: EmbedSalt: %v", value, err)
		}

		step := byte(1)
		if value == 255 {
			step = 254
		}
		for i := 0; i < len(img.Pix); i += 4 {
			for c := 0; c < 3; c++ {
				got := img.Pix[i+c]
				if got != value && got != step {
					t.Fatalf("value %d: channel %d became %d", value, i+c, got)
				}
			}
		}
	}
}

func TestSaltTinyCarrier(t *testing.T) {
	img := solidNRGBA(1, 1, 128) // T = 3, far below the 128 salt channels
	if err := stego.EmbedSalt(img, make([]byte, 16)); !errors.Is(err, stego.ErrTooSmall) {
		t.Fatalf("EmbedSalt on 1x1 = %v, want ErrTooSmall", err)
	}
	if _, err := stego.ExtractSalt(img, 16); !errors.Is(err, stego.ErrTooSmall) {
		t.Fatalf("ExtractSalt on 1x1 = %v, want ErrTooSmall", err)
	}
}

func TestEmbedBytesBeyondCapacity(t *testing.T) {
	img := solidNRGBA(4, 4, 128) // T = 48
	pos := stego.SaltPositions(stego.ChannelCount(img))
	if len(pos) != 48 {
		t.Fatalf("len(S) = %d, want 48", len(pos))
	}

	if err := stego.EmbedBytes(img, pos, make([]byte, 7)); !errors.Is(err, stego.ErrTooSmall) {
		t.Fatalf("7 bytes into 48 channels = %v, want ErrTooSmall", err)
	}
	if err := stego.EmbedBytes(img, pos, make([]byte, 6)); err != nil {
		t.Fatalf("6 bytes into 48 channels: %v", err)
	}
}

func TestEmbedExtractAllChannels(t *testing.T) {
	img := costVectorImage(8, 8) // T = 192 -> 24 bytes exactly
	all := make([]uint32, stego.ChannelCount(img))
	for i := range all {
		all[i] = uint32(i)
	}

	data := make([]byte, len(all)/8)
	for i := range data {
		data[i] = byte(i*11 + 3)
	}

	if err := stego.EmbedBytes(img, all, data); err != nil {
		t.Fatalf("EmbedBytes: %v", err)
	}
	got, err := stego.ExtractBytes(img, all, len(data))
	if err != nil {
		t.Fatalf("ExtractBytes: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip = %x, want %x", got, data)
	}
}

func TestEmbedBitsRejectsOutOfRangePosition(t *testing.T) {
	img := solidNRGBA(2, 2, 128) // T = 12
	err := stego.EmbedBits(img, []uint32{0, 12}, []byte{1, 0})
	if !errors.Is(err, stego.ErrBadPosition) {
		t.Fatalf("EmbedBits = %v, want ErrBadPosition", err)
	}
}

// Ensure the package keeps accepting a plain *image.NRGBA with a non-zero
// origin, because carrier normalises to (0,0) but callers may not.
func TestChannelOffsetNonZeroOrigin(t *testing.T) {
	img := image.NewNRGBA(image.Rect(10, 20, 12, 22))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xFF
	}
	stego.SetLSB(img, 0, 1)
	if got := stego.LSB(img, 0); got != 1 {
		t.Fatalf("LSB with offset origin = %d, want 1", got)
	}
}
