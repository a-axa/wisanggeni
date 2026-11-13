package stego

import "image"

// CoverVerdict is the §7.5 classification of a carrier's textured_fraction.
type CoverVerdict int

const (
	// CoverSuitable means textured_fraction >= TexturedFractionGood.
	CoverSuitable CoverVerdict = iota

	// CoverMarginal means TexturedFractionWarn <= textured_fraction <
	// TexturedFractionGood: usable, but the caller should warn.
	CoverMarginal

	// CoverUnsuitable means textured_fraction < TexturedFractionWarn: the
	// caller should refuse unless forced (exit 5).
	CoverUnsuitable
)

// String returns the lowercase label used by the CLI and the format document.
func (v CoverVerdict) String() string {
	switch v {
	case CoverSuitable:
		return "suitable"
	case CoverMarginal:
		return "warning"
	case CoverUnsuitable:
		return "reject"
	default:
		return "unknown"
	}
}

// CoverStats is the result of CoverScore (§7.5).
type CoverStats struct {
	// TexturedFraction is the share of channels whose smoothed residual S
	// exceeds TexturedSThreshold.
	TexturedFraction float64

	// MedianRho is the median cost over every channel.
	MedianRho float64

	// Verdict classifies TexturedFraction.
	Verdict CoverVerdict
}

// CoverScore rates how well a carrier can hide a payload (§7.5): too smooth an
// image leaves the embedding visible, so it is rejected. The score is a filter
// on textured_fraction, not a guarantee of undetectability.
//
// It reuses the cost map engine, so the cost slice it builds is consumed by the
// median selection in place and is not returned. A nil or empty carrier scores
// as unsuitable with zero fractions.
func CoverScore(img *image.NRGBA) CoverStats {
	costs, textured := costMap(img)
	total := len(costs)
	if total == 0 {
		return CoverStats{Verdict: CoverUnsuitable}
	}

	stats := CoverStats{
		TexturedFraction: float64(textured) / float64(total),
		MedianRho:        medianFloat64(costs),
		Verdict:          classifyCover(float64(textured) / float64(total)),
	}
	return stats
}

// classifyCover applies the §7.5 thresholds to a textured_fraction.
func classifyCover(texturedFraction float64) CoverVerdict {
	switch {
	case texturedFraction >= TexturedFractionGood:
		return CoverSuitable
	case texturedFraction >= TexturedFractionWarn:
		return CoverMarginal
	default:
		return CoverUnsuitable
	}
}

// medianFloat64 returns the median of values, reordering the slice in place.
// Selection is quickselect, so the cost of a large carrier stays linear in the
// number of channels and no second buffer is allocated.
func medianFloat64(values []float64) float64 {
	n := len(values)
	if n == 0 {
		return 0
	}

	mid := n / 2
	quickSelect(values, mid)
	if n%2 == 1 {
		return values[mid]
	}

	// Even count: the median is the mean of the two central values. After
	// selecting index mid, the lower neighbour is the largest of the prefix.
	low := values[0]
	for _, v := range values[:mid] {
		if v > low {
			low = v
		}
	}
	return (low + values[mid]) / 2
}

// quickSelect places the k-th smallest value of a at a[k]. It is a three-way
// partition, so carriers where every channel has the same cost (a flat image)
// still finish in one pass instead of degenerating to quadratic time.
func quickSelect(a []float64, k int) {
	lo, hi := 0, len(a)-1
	for lo < hi {
		pivot := a[(lo+hi)/2]

		lt, i, gt := lo, lo, hi
		for i <= gt {
			switch {
			case a[i] < pivot:
				a[lt], a[i] = a[i], a[lt]
				lt++
				i++
			case a[i] > pivot:
				a[gt], a[i] = a[i], a[gt]
				gt--
			default:
				i++
			}
		}

		switch {
		case k < lt:
			hi = lt - 1
		case k > gt:
			lo = gt + 1
		default:
			return
		}
	}
}
