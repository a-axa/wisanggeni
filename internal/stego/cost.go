package stego

import (
	"image"
	"math"
)

// CostMap computes the HILL-inspired per-channel embedding cost (§7.3) from a
// normalised carrier. The result is indexed by channel, pixel*3 + {R,G,B}.
//
// This is the reference implementation: float64 throughout and the operation
// order of §7.3, so the golden vector in the tests pins the exact values. A
// caller that must bound memory (B3) may store the result as float32, but the
// arithmetic below stays float64.
//
// Edge handling is frozen as mirror padding without repeating the edge pixel:
// index -1 maps to 1 and index n maps to n-2, and a dimension of 1 maps every
// index to 0. Every window position therefore refers to a real pixel, and the
// box means divide by the full window (9 and 25).
func CostMap(img *image.NRGBA) []float64 {
	costs, _ := costMap(img)
	return costs
}

// costMap is the shared engine behind CostMap and CoverScore. Besides the cost
// map it returns the number of channels whose smoothed residual S exceeds
// TexturedSThreshold, which cover scoring needs and which would otherwise force
// a second pass over the carrier (§7.5).
func costMap(img *image.NRGBA) ([]float64, int) {
	if img == nil {
		return nil, 0
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	n := w * h
	costs := make([]float64, n*3)
	if n == 0 {
		return costs, 0
	}

	// textured counts channels with S > TexturedSThreshold across all planes.
	textured := 0

	plane := make([]float64, n)
	residual := make([]float64, n)
	partial := make([]float64, n)
	smoothed := make([]float64, n)

	for c := 0; c < 3; c++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				plane[y*w+x] = float64(nrgbaComponent(img, b.Min.X+x, b.Min.Y+y, c))
			}
		}

		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var sum float64
				for ky := -1; ky <= 1; ky++ {
					row := mirrorIndex(y+ky, h) * w
					for kx := -1; kx <= 1; kx++ {
						sum += CostKernel[ky+1][kx+1] * plane[row+mirrorIndex(x+kx, w)]
					}
				}
				residual[y*w+x] = math.Abs(sum)
			}
		}

		boxMean(residual, partial, w, h, 1)
		boxMean(partial, smoothed, w, h, 2)

		for i := 0; i < n; i++ {
			rho := 1 / (smoothed[i] + CostEpsilon)
			if edge := plane[i]; edge == 0 || edge == 255 {
				rho *= CostEdgeMultiplier
			}
			costs[i*3+c] = rho
			if smoothed[i] > TexturedSThreshold {
				textured++
			}
		}
	}

	return costs, textured
}

func nrgbaComponent(img *image.NRGBA, x, y, c int) uint8 {
	px := img.NRGBAAt(x, y)
	switch c {
	case 0:
		return px.R
	case 1:
		return px.G
	default:
		return px.B
	}
}

// mirrorIndex maps an index outside [0,n) back inside without repeating the
// edge pixel, so a 3x3 window at a corner still averages nine real pixels.
func mirrorIndex(i, n int) int {
	if n == 1 {
		return 0
	}
	for i < 0 || i >= n {
		if i < 0 {
			i = -i
		}
		if i >= n {
			i = 2*(n-1) - i
		}
	}
	return i
}

// boxMean writes the mean of the (2r+1)^2 mirrored window around each pixel.
func boxMean(src, dst []float64, w, h, radius int) {
	side := 2*radius + 1
	divisor := float64(side * side)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sum float64
			for dy := -radius; dy <= radius; dy++ {
				row := mirrorIndex(y+dy, h) * w
				for dx := -radius; dx <= radius; dx++ {
					sum += src[row+mirrorIndex(x+dx, w)]
				}
			}
			dst[y*w+x] = sum / divisor
		}
	}
}
