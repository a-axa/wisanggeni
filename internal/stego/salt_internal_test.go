package stego

import (
	"image"
	"testing"
)

func TestMatchLSB(t *testing.T) {
	cases := []struct {
		v, bit byte
		up     bool
		want   byte
	}{
		{128, 0, true, 128},  // already matching stays put
		{128, 1, true, 129},  // move up
		{128, 1, false, 127}, // move down
		{1, 1, true, 1},      // already matching stays put
		{1, 0, true, 2},      // move up from odd
		{1, 0, false, 0},     // move down from odd
		{0, 1, true, 1},      // zero can only go up
		{0, 1, false, 1},     // zero ignores direction
		{255, 0, true, 254},  // 255 can only go down
		{255, 0, false, 254}, // 255 ignores direction
	}
	for _, tc := range cases {
		if got := matchLSB(tc.v, tc.bit, tc.up); got != tc.want {
			t.Errorf("matchLSB(%d, %d, %v) = %d, want %d", tc.v, tc.bit, tc.up, got, tc.want)
		}
	}
}

// TestEmbedBitsDirections pins which way a value moves. Channels 0..5 cover two
// pixels, so the alpha bytes sit at Pix[3] and Pix[7] and must stay untouched.
func TestEmbedBitsDirections(t *testing.T) {
	positions := []uint32{0, 1, 2, 3, 4, 5}

	newImg := func() *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
		values := []byte{100, 101, 102, 103, 104, 105}
		img.Pix[0], img.Pix[1], img.Pix[2] = values[0], values[1], values[2]
		img.Pix[4], img.Pix[5], img.Pix[6] = values[3], values[4], values[5]
		img.Pix[3], img.Pix[7] = 0xFF, 0xFF
		return img
	}
	payload := []byte{1, 0, 1, 0, 1, 0}

	up := newImg()
	if err := embedBits(up, positions, payload, func() bool { return true }); err != nil {
		t.Fatalf("embedBits up: %v", err)
	}
	wantUp := []byte{101, 102, 103, 104, 105, 106}
	if got := []byte{up.Pix[0], up.Pix[1], up.Pix[2], up.Pix[4], up.Pix[5], up.Pix[6]}; string(got) != string(wantUp) {
		t.Errorf("up: values = %v, want %v", got, wantUp)
	}
	if up.Pix[3] != 0xFF || up.Pix[7] != 0xFF {
		t.Error("up: alpha changed")
	}

	down := newImg()
	if err := embedBits(down, positions, payload, func() bool { return false }); err != nil {
		t.Fatalf("embedBits down: %v", err)
	}
	wantDown := []byte{99, 100, 101, 102, 103, 104}
	if got := []byte{down.Pix[0], down.Pix[1], down.Pix[2], down.Pix[4], down.Pix[5], down.Pix[6]}; string(got) != string(wantDown) {
		t.Errorf("down: values = %v, want %v", got, wantDown)
	}
	if down.Pix[3] != 0xFF || down.Pix[7] != 0xFF {
		t.Error("down: alpha changed")
	}
}
