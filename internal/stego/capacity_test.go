package stego_test

import (
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// The table in §6.1 is verified arithmetic, not an illustration. If any of
// these change, the format changed.
func TestCapacityTable(t *testing.T) {
	cases := []struct {
		w, h                           int
		raw, usable, suggested, strong int64
	}{
		{1920, 1080, 777600, 777488, 77648, 38768},
		{1080, 1350, 546750, 546638, 54563, 27225},
		{3840, 2160, 3110400, 3110288, 310928, 155408},
		{1, 1, 0, 0, 0, 0},
	}

	for _, tc := range cases {
		if got := stego.CapacityRaw(tc.w, tc.h); got != tc.raw {
			t.Errorf("CapacityRaw(%d,%d) = %d, want %d", tc.w, tc.h, got, tc.raw)
		}
		if got := stego.Capacity(tc.w, tc.h); got != tc.usable {
			t.Errorf("Capacity(%d,%d) = %d, want %d", tc.w, tc.h, got, tc.usable)
		}
		if got := stego.SuggestedCapacity(tc.w, tc.h); got != tc.suggested {
			t.Errorf("SuggestedCapacity(%d,%d) = %d, want %d", tc.w, tc.h, got, tc.suggested)
		}
		if got := stego.StrongCapacity(tc.w, tc.h); got != tc.strong {
			t.Errorf("StrongCapacity(%d,%d) = %d, want %d", tc.w, tc.h, got, tc.strong)
		}
	}
}

func TestCapacityNeverNegative(t *testing.T) {
	for _, dim := range [][2]int{{0, 0}, {0, 10}, {10, 0}, {-1, 5}, {1, 1}, {2, 2}} {
		if got := stego.Capacity(dim[0], dim[1]); got != 0 {
			t.Errorf("Capacity(%d,%d) = %d, want 0", dim[0], dim[1], got)
		}
		if got := stego.SuggestedCapacity(dim[0], dim[1]); got != 0 {
			t.Errorf("SuggestedCapacity(%d,%d) = %d, want 0", dim[0], dim[1], got)
		}
		if got := stego.StrongCapacity(dim[0], dim[1]); got != 0 {
			t.Errorf("StrongCapacity(%d,%d) = %d, want 0", dim[0], dim[1], got)
		}
	}
}

func TestFillRatio(t *testing.T) {
	const w, h = 1920, 1080
	channels := stego.Channels(w, h)

	// A blob that fills every channel bit.
	if got := stego.FillRatio(stego.CapacityRaw(w, h), w, h); got != 1 {
		t.Errorf("full fill = %v, want 1", got)
	}

	// Filling the suggested capacity lands exactly on 10%. The suggested number
	// already subtracts the container overhead, so the blob adds it back.
	blobAtSuggested := stego.SuggestedCapacity(w, h) + format.BlobOverhead
	if got := stego.FillRatio(blobAtSuggested, w, h); got != 0.1 {
		t.Errorf("suggested fill = %v, want 0.1", got)
	}

	if got := stego.FillRatio(0, w, h); got != 0 {
		t.Errorf("empty fill = %v, want 0", got)
	}
	if got := stego.FillRatio(1, 0, 0); got != 0 {
		t.Errorf("fill of empty carrier = %v, want 0", got)
	}

	// The policy thresholds of §5 sit at 5%, 10% and 50%.
	if stego.FillWarn != 0.05 || stego.FillForce != 0.10 || stego.FillReject != 0.50 {
		t.Errorf("fill thresholds = %v/%v/%v, want 0.05/0.10/0.50",
			stego.FillWarn, stego.FillForce, stego.FillReject)
	}
	if channels != int64(w)*int64(h)*3 {
		t.Errorf("Channels(%d,%d) = %d, inconsistent with W*H*3", w, h, channels)
	}
}
