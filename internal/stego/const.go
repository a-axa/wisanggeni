// Package stego implements the adaptive embedding layer: bit I/O, keyed
// permutation, cost map, STC, capacity and verification.
//
// The constants in this file are part of format v1 (§6.1) and must not change
// after v1.0.0. The algorithms that consume them arrive in Fase B.
package stego

const (
	// SaltChannels is how many channels the salt occupies, at the public
	// positions S (§7.2).
	SaltChannels = 128

	// STCHeight is the number of parity-check rows per trellis section, giving
	// 2^8 Viterbi states (§10).
	STCHeight = 8

	// STCWidth is the number of columns per parity-check submatrix. Together
	// with STCHeight it sets the code rate h/w = 1/2 bit per channel (§10).
	STCWidth = 16

	// STCBlockChannels is how many pool channels are buffered at a time when
	// hide/reveal walk P'; the code itself is block-local at STCWidth (§10).
	STCBlockChannels = 65536

	// DenseDivisor is the n/10 threshold that selects the dense shuffle variant
	// over the sparse map variant (§7.1).
	DenseDivisor = 10
)

// Cost map and cover scoring (§7.3, §7.5).
const (
	// CostEpsilon keeps rho finite on a perfectly flat carrier.
	CostEpsilon = 10.0

	// CostEdgeMultiplier penalises channels at 0 and 255, where LSB matching
	// has only one direction to move.
	CostEdgeMultiplier = 4.0

	// TexturedSThreshold is the smoothed residual above which a channel counts
	// as textured.
	TexturedSThreshold = 16.0

	// Cover classification thresholds on textured_fraction.
	TexturedFractionGood = 0.20
	TexturedFractionWarn = 0.05
)

// Fill policy (§5). These are warnings, not guarantees.
const (
	FillWarn   = 0.05
	FillForce  = 0.10
	FillReject = 0.50
)

// CostKernel is the high-pass residual kernel from §7.3.
var CostKernel = [3][3]float64{
	{-1, 2, -1},
	{2, -4, 2},
	{-1, 2, -1},
}
