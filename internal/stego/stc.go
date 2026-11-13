// Package stego implements the adaptive embedding layer: bit I/O, keyed
// permutation, cost map, STC, capacity and verification.
//
// STC is the syndrome-trellis code of §10: a block of STCWidth pool channels
// carries STCHeight = 8 message bits, so the code rate is 1/2 bit per channel.
// Every block is solved exactly — the parity-check matrix is small enough that
// the whole affine solution space (2^(w-h) = 256 candidates) can be enumerated
// and the cheapest one taken, which is what makes the distortion provably
// minimal for the given costs.
package stego

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"math/rand/v2"

	"github.com/a-axa/wisanggeni/internal/format"
)

// ErrSTC reports an embedding that could not satisfy its syndrome constraint.
// It cannot happen with a full-rank parity matrix, so it signals a code bug
// rather than bad input.
var ErrSTC = errors.New("stego: STC embedding failed")

// ErrCostMap reports a cost map that does not cover every channel.
var ErrCostMap = errors.New("stego: cost map is smaller than the carrier")

// parityCode is the frozen parity-check matrix plus the precomputed pieces
// needed to solve Ĥ·e = s over GF(2).
type parityCode struct {
	// cols[i] is column i of Ĥ as an 8-bit syndrome mask.
	cols [STCWidth]byte

	// solve maps a syndrome to the flip mask over the basis columns only.
	solve [1 << STCHeight]uint16

	// nulls span the kernel of Ĥ: every solution of Ĥ·e = s is
	// solve[s] XOR any subset of nulls.
	nulls [STCWidth - STCHeight]uint16
}

// stcCode is built once. The matrix is derived from a fixed label, so a rank
// failure is a defect in this package and not a runtime condition; the panic is
// unreachable as long as TestParityMatrixIsFullRank passes.
var stcCode = mustBuildParityCode()

func mustBuildParityCode() *parityCode {
	cols := deriveParityColumns()

	p := &parityCode{cols: cols}

	// Greedily collect independent columns while tracking, for every reachable
	// syndrome, one combination of basis columns that produces it. A column
	// already in the span is dependent and becomes a kernel generator.
	var basis []int
	span := []int{0} // syndromes reachable with the basis chosen so far
	reachable := make(map[int]bool, 1<<STCHeight)
	reachable[0] = true

	for c := 0; c < STCWidth && len(basis) < STCHeight; c++ {
		v := int(cols[c])
		if reachable[v] {
			continue
		}
		// Snapshot the current span: appending to span while ranging over it
		// would revisit the new syndromes and apply the column twice.
		current := span
		span = make([]int, 0, 2*len(current))
		span = append(span, current...)
		for _, s := range current {
			ns := s ^ v
			reachable[ns] = true
			p.solve[ns] = p.solve[s] | 1<<uint(c)
			span = append(span, ns)
		}
		basis = append(basis, c)
	}
	if len(basis) < STCHeight {
		panic("stego: STC parity matrix is rank deficient")
	}

	n := 0
	for c := 0; c < STCWidth; c++ {
		if isBasis(basis, c) {
			continue
		}
		// The kernel generator for a dependent column c is c itself plus the
		// basis combination that reproduces its column.
		p.nulls[n] = 1<<uint(c) | p.solve[cols[c]]
		n++
	}
	if n != len(p.nulls) {
		panic("stego: unexpected STC kernel dimension")
	}

	return p
}

// deriveParityColumns builds Ĥ from the public label of §10: ChaCha8 seeded by
// SHA-256("wisanggeni/v1/stc"), taking one byte per column from the raw stream.
func deriveParityColumns() [STCWidth]byte {
	rng := rand.NewChaCha8(sha256.Sum256([]byte(format.LabelSTC)))

	var cols [STCWidth]byte
	for i := range cols {
		cols[i] = byte(rng.Uint64())
	}
	return cols
}

func isBasis(basis []int, c int) bool {
	for _, b := range basis {
		if b == c {
			return true
		}
	}
	return false
}

// ParityColumns returns a copy of the parity-check columns, for tests and the
// reference implementation.
func ParityColumns() [STCWidth]byte {
	return stcCode.cols
}

// syndrome accumulates the columns the mask selects.
func (p *parityCode) syndrome(mask uint16) byte {
	var s byte
	for i := 0; i < STCWidth; i++ {
		if mask&(1<<uint(i)) != 0 {
			s ^= p.cols[i]
		}
	}
	return s
}

// solveMask returns the cheapest flip mask whose syndrome is target, breaking
// ties by preferring no flip at the earliest column. It enumerates the whole
// affine solution space, so the result is a global optimum for the block.
func (p *parityCode) solveMask(target byte, costs *[STCWidth]float64) (uint16, float64) {
	part := p.solve[target]
	best := part
	bestCost := maskCost(part, costs)

	for j := 1; j < 1<<uint(len(p.nulls)); j++ {
		mask := part
		for b, nv := range p.nulls {
			if j&(1<<uint(b)) != 0 {
				mask ^= nv
			}
		}
		cost := maskCost(mask, costs)
		if cost < bestCost || (cost == bestCost && maskLess(mask, best)) {
			best, bestCost = mask, cost
		}
	}
	return best, bestCost
}

func maskCost(mask uint16, costs *[STCWidth]float64) float64 {
	var cost float64
	for i := 0; i < STCWidth; i++ {
		if mask&(1<<uint(i)) != 0 {
			cost += costs[i]
		}
	}
	return cost
}

// maskLess orders masks by the lexicographic order of their per-column
// decisions, earliest column first: a mask that leaves column 0 untouched wins.
func maskLess(a, b uint16) bool {
	for i := 0; i < STCWidth; i++ {
		ab := (a >> uint(i)) & 1
		bb := (b >> uint(i)) & 1
		if ab != bb {
			return ab == 0
		}
	}
	return false
}

// EmbedSTC writes data into the pool channels named by positions, one message
// byte per block of STCWidth channels (§7.4, §10).
//
// costs is the cost map indexed by channel, as CostMap returns it. len(positions)
// must cover STCWidth channels per byte of data, and no position may repeat a
// salt channel — a prefix of PoolPositions satisfies both.
func EmbedSTC(img *image.NRGBA, positions []uint32, data []byte, costs []float64) error {
	if len(data) == 0 {
		return nil
	}

	channels := ChannelCount(img)
	if channels == 0 {
		return ErrTooSmall
	}
	if len(positions) < len(data)*STCWidth {
		return fmt.Errorf("%w: %d positions for %d bytes", ErrTooSmall, len(positions), len(data))
	}
	if len(costs) < int(channels) {
		return fmt.Errorf("%w: %d costs for %d channels", ErrCostMap, len(costs), channels)
	}

	for b := 0; b < len(data); b++ {
		base := b * STCWidth

		var (
			blockCosts [STCWidth]float64
			coverBits  uint16
		)
		for i := 0; i < STCWidth; i++ {
			c := positions[base+i]
			if c >= channels {
				return fmt.Errorf("%w: channel %d of %d", ErrBadPosition, c, channels)
			}
			blockCosts[i] = costs[c]
			if LSB(img, c) == 1 {
				coverBits |= 1 << uint(i)
			}
		}

		// The stego LSB vector must have syndrome data[b], and flipping the mask
		// toggles exactly those syndrome contributions:
		//   H*(cover XOR mask) = data[b]  =>  H*mask = data[b] XOR H*cover.
		target := data[b] ^ stcCode.syndrome(coverBits)
		mask, _ := stcCode.solveMask(target, &blockCosts)
		if got := stcCode.syndrome(mask); got != target {
			return fmt.Errorf("%w: syndrome %#x for target %#x", ErrSTC, got, target)
		}

		for i := 0; i < STCWidth; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			off := channelOffset(img, positions[base+i])
			// matchLSB takes the bit the channel must end up with.
			want := byte(1) - byte((coverBits>>uint(i))&1)
			img.Pix[off] = matchLSB(img.Pix[off], want, randomFlip())
		}
	}
	return nil
}

// ExtractSTC reads size message bytes back from the pool channels named by
// positions. It needs no cost map and no cover: the message is the syndrome of
// the stego LSB vector.
func ExtractSTC(img *image.NRGBA, positions []uint32, size int) ([]byte, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: negative size %d", ErrTooSmall, size)
	}
	if size == 0 {
		return nil, nil
	}

	channels := ChannelCount(img)
	if channels == 0 {
		return nil, ErrTooSmall
	}
	if len(positions) < size*STCWidth {
		return nil, fmt.Errorf("%w: %d positions for %d bytes", ErrTooSmall, len(positions), size)
	}

	out := make([]byte, size)
	for b := 0; b < size; b++ {
		base := b * STCWidth
		var syndrome byte
		for i := 0; i < STCWidth; i++ {
			c := positions[base+i]
			if c >= channels {
				return nil, fmt.Errorf("%w: channel %d of %d", ErrBadPosition, c, channels)
			}
			if LSB(img, c) == 1 {
				syndrome ^= stcCode.cols[i]
			}
		}
		out[b] = syndrome
	}
	return out, nil
}

// STCCost returns the total distortion an embed would pay for data at the given
// positions, without touching the image. Tests use it to compare STC against
// uniform LSB matching.
func STCCost(img *image.NRGBA, positions []uint32, data []byte, costs []float64) (float64, error) {
	channels := ChannelCount(img)
	if channels == 0 {
		return 0, ErrTooSmall
	}
	if len(positions) < len(data)*STCWidth {
		return 0, fmt.Errorf("%w: %d positions for %d bytes", ErrTooSmall, len(positions), len(data))
	}
	if len(costs) < int(channels) {
		return 0, fmt.Errorf("%w: %d costs for %d channels", ErrCostMap, len(costs), channels)
	}

	var total float64
	for b := 0; b < len(data); b++ {
		var (
			blockCosts [STCWidth]float64
			coverBits  uint16
		)
		base := b * STCWidth
		for i := 0; i < STCWidth; i++ {
			c := positions[base+i]
			if c >= channels {
				return 0, fmt.Errorf("%w: channel %d of %d", ErrBadPosition, c, channels)
			}
			blockCosts[i] = costs[c]
			if LSB(img, c) == 1 {
				coverBits |= 1 << uint(i)
			}
		}
		target := data[b] ^ stcCode.syndrome(coverBits)
		mask, blockCost := stcCode.solveMask(target, &blockCosts)
		if s := stcCode.syndrome(mask); s != target {
			return 0, fmt.Errorf("%w: syndrome %#x for target %#x", ErrSTC, s, target)
		}
		total += blockCost
	}
	return total, nil
}

// MaxSTCBytes returns how many message bytes fit in n pool channels.
func MaxSTCBytes(n int) int {
	if n < STCWidth {
		return 0
	}
	return n / STCWidth
}
