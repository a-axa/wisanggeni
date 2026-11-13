package stego

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

// The parity matrix is frozen: it is derived from a public label and any change
// would silently alter every hidden payload. The value is also consumed by
// detector/stc_ref.py, which must stay in lockstep.
var goldenParityColumns = [STCWidth]byte{
	0x84, 0x3F, 0x8F, 0x45, 0x97, 0x5E, 0x3E, 0x0E,
	0x0E, 0x20, 0x00, 0xA7, 0x4E, 0x7E, 0x25, 0x0A,
}

func TestParityMatrixGolden(t *testing.T) {
	if got := deriveParityColumns(); got != goldenParityColumns {
		t.Fatalf("derived parity columns = %#v, want %#v", got, goldenParityColumns)
	}
	if got := ParityColumns(); got != goldenParityColumns {
		t.Fatalf("ParityColumns() = %#v, want %#v", got, goldenParityColumns)
	}
}

type stcVectorFile struct {
	Columns []byte    `json:"columns"`
	Cases   []stcCase `json:"cases"`
}

type stcCase struct {
	CoverBits      []byte    `json:"cover_bits"`
	Costs          []float64 `json:"costs"`
	Message        byte      `json:"message"`
	SyndromeTarget byte      `json:"syndrome_target"`
	ExpectedMask   uint16    `json:"expected_mask"`
	ExpectedCost   float64   `json:"expected_cost"`
}

// TestSTCVectors checks the Go solver against vectors produced independently by
// detector/stc_ref.py, which brute-forces the whole mask space.
func TestSTCVectors(t *testing.T) {
	path := filepath.Join("testdata", "stc_vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}

	var file stcVectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}

	if len(file.Columns) != STCWidth {
		t.Fatalf("vector columns = %d, want %d", len(file.Columns), STCWidth)
	}
	for i, col := range file.Columns {
		if col != goldenParityColumns[i] {
			t.Fatalf("vector column %d = %#x, want %#x", i, col, goldenParityColumns[i])
		}
	}

	for i, tc := range file.Cases {
		var (
			costs     [STCWidth]float64
			coverMask uint16
		)
		if len(tc.Costs) != STCWidth || len(tc.CoverBits) != STCWidth {
			t.Fatalf("case %d: malformed vector", i)
		}
		for j := range costs {
			costs[j] = tc.Costs[j]
			if tc.CoverBits[j] == 1 {
				coverMask |= 1 << uint(j)
			}
		}

		if want := tc.Message ^ stcCode.syndrome(coverMask); want != tc.SyndromeTarget {
			t.Fatalf("case %d: vector target %#x, recomputed %#x", i, tc.SyndromeTarget, want)
		}

		mask, cost := stcCode.solveMask(tc.SyndromeTarget, &costs)
		if mask != tc.ExpectedMask {
			t.Errorf("case %d: mask = %#016b, want %#016b", i, mask, tc.ExpectedMask)
		}
		if math.Abs(cost-tc.ExpectedCost) > 1e-9 {
			t.Errorf("case %d: cost = %.9f, want %.9f", i, cost, tc.ExpectedCost)
		}
	}
}

// TestSTCSolveMaskBruteForce cross-checks the affine-space solver against an
// exhaustive scan written straight from the definition.
func TestSTCSolveMaskBruteForce(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 100))

	for trial := 0; trial < 8; trial++ {
		var costs [STCWidth]float64
		for i := range costs {
			costs[i] = 0.01 + rng.Float64()*4
		}
		target := byte(rng.IntN(256))

		wantMask, wantCost := bruteForceMask(target, &costs)
		gotMask, gotCost := stcCode.solveMask(target, &costs)

		if gotMask != wantMask {
			t.Fatalf("trial %d target %#x: mask = %#016b, want %#016b", trial, target, gotMask, wantMask)
		}
		if gotCost != wantCost {
			t.Fatalf("trial %d target %#x: cost = %.17g, want %.17g", trial, target, gotCost, wantCost)
		}
	}
}

func bruteForceMask(target byte, costs *[STCWidth]float64) (uint16, float64) {
	best := uint16(0)
	bestCost := math.Inf(1)
	for mask := uint16(0); ; mask++ {
		if stcCode.syndrome(mask) == target {
			cost := maskCost(mask, costs)
			if cost < bestCost || (cost == bestCost && maskLess(mask, best)) {
				best, bestCost = mask, cost
			}
		}
		if mask == 1<<STCWidth-1 {
			break
		}
	}
	return best, bestCost
}
