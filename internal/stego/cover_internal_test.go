package stego

import (
	"math/rand/v2"
	"testing"
)

// TestClassifyCoverBoundaries pins the §7.5 thresholds, including the closed
// lower bounds, so an off-by-one in the comparison cannot pass unnoticed.
func TestClassifyCoverBoundaries(t *testing.T) {
	cases := []struct {
		fraction float64
		want     CoverVerdict
	}{
		{0, CoverUnsuitable},
		{0.0499, CoverUnsuitable},
		{0.05, CoverMarginal},
		{0.1999, CoverMarginal},
		{0.20, CoverSuitable},
		{1, CoverSuitable},
	}
	for _, tc := range cases {
		if got := classifyCover(tc.fraction); got != tc.want {
			t.Errorf("classifyCover(%v) = %v, want %v", tc.fraction, got, tc.want)
		}
	}
}

func TestMedianFloat64(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{"empty", nil, 0},
		{"single", []float64{7}, 7},
		{"odd unsorted", []float64{9, 1, 5, 3, 7}, 5},
		{"even unsorted", []float64{9, 1, 5, 3, 7, 11}, 6},
		{"all equal", []float64{4, 4, 4, 4}, 4},
		{"two", []float64{2, 8}, 5},
	}
	for _, tc := range cases {
		got := medianFloat64(append([]float64(nil), tc.values...))
		if got != tc.want {
			t.Errorf("%s: median = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestMedianFloat64AgainstSort checks the quickselect median against a plain
// reference implementation on random inputs, so a partition bug cannot hide
// behind hand-picked values.
func TestMedianFloat64AgainstSort(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))

	for trial := 0; trial < 200; trial++ {
		n := 1 + rng.IntN(64)
		values := make([]float64, n)
		for i := range values {
			// Coarse values force duplicates, exercising the three-way split.
			values[i] = float64(rng.IntN(8))
		}

		want := referenceMedian(append([]float64(nil), values...))
		got := medianFloat64(append([]float64(nil), values...))
		if got != want {
			t.Fatalf("trial %d: median = %v, want %v (values %v)", trial, got, want, values)
		}
	}
}

func referenceMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	// Insertion sort: n is small and correctness matters more than speed here.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
