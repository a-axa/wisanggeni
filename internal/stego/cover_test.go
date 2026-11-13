package stego_test

import (
	"math/rand/v2"
	"testing"

	"github.com/a-axa/wisanggeni/internal/stego"
)

func TestCoverScoreFlatIsUnsuitable(t *testing.T) {
	// A perfectly flat carrier has S = 0 everywhere, so nothing is textured;
	// every channel costs 1/CostEpsilon.
	stats := stego.CoverScore(solidNRGBA(16, 16, 128))

	if stats.TexturedFraction != 0 {
		t.Errorf("flat textured_fraction = %v, want 0", stats.TexturedFraction)
	}
	if want := 1 / stego.CostEpsilon; stats.MedianRho != want {
		t.Errorf("flat median_rho = %v, want %v", stats.MedianRho, want)
	}
	if stats.Verdict != stego.CoverUnsuitable {
		t.Errorf("flat verdict = %v, want %v", stats.Verdict, stego.CoverUnsuitable)
	}
}

func TestCoverScoreNoiseIsSuitable(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 22))
	img := randomNRGBA(rng, 64, 64)

	stats := stego.CoverScore(img)

	if stats.TexturedFraction < stego.TexturedFractionGood {
		t.Errorf("noise textured_fraction = %v, want >= %v",
			stats.TexturedFraction, stego.TexturedFractionGood)
	}
	if stats.Verdict != stego.CoverSuitable {
		t.Errorf("noise verdict = %v, want %v", stats.Verdict, stego.CoverSuitable)
	}
	if stats.MedianRho <= 0 {
		t.Errorf("noise median_rho = %v, want > 0", stats.MedianRho)
	}
}

func TestCoverScoreVectorPattern(t *testing.T) {
	// Frozen vector: the 8x8 pattern of §8.1. The 5x5 smoothing spreads the
	// boundary residual over the flat half, so on an 8x8 tile every channel
	// ends up textured; the median cost is dominated by the flat, dear half.
	stats := stego.CoverScore(costVectorImage(8, 8))

	if stats.TexturedFraction != 1 {
		t.Errorf("textured_fraction = %v, want 1", stats.TexturedFraction)
	}
	if want := 0.0042531553983439184; stats.MedianRho != want {
		t.Errorf("median_rho = %.17g, want %.17g", stats.MedianRho, want)
	}
	if stats.Verdict != stego.CoverSuitable {
		t.Errorf("verdict = %v, want %v", stats.Verdict, stego.CoverSuitable)
	}
}

func TestCoverScoreEmptyIsUnsuitable(t *testing.T) {
	for _, stats := range []stego.CoverStats{
		stego.CoverScore(nil),
		stego.CoverScore(solidNRGBA(0, 0, 128)),
	} {
		if stats.TexturedFraction != 0 || stats.MedianRho != 0 {
			t.Errorf("empty score = %+v, want zero fractions", stats)
		}
		if stats.Verdict != stego.CoverUnsuitable {
			t.Errorf("empty verdict = %v, want %v", stats.Verdict, stego.CoverUnsuitable)
		}
	}
}

func TestCoverVerdictString(t *testing.T) {
	cases := map[stego.CoverVerdict]string{
		stego.CoverSuitable:   "suitable",
		stego.CoverMarginal:   "warning",
		stego.CoverUnsuitable: "reject",
	}
	for verdict, want := range cases {
		if got := verdict.String(); got != want {
			t.Errorf("Verdict(%d).String() = %q, want %q", verdict, got, want)
		}
	}
	if got := stego.CoverVerdict(99).String(); got != "unknown" {
		t.Errorf("out-of-range verdict = %q, want \"unknown\"", got)
	}
}
