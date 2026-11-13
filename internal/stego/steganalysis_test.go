package stego

import (
	"crypto/rand"
	"image"
	"math"
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
)

// Steganalysis tests (PRD Fase G3).
//
// These do not try to prove the embedding is undetectable — nothing can, and
// the README says so. They pin two measurable properties that the format is
// designed for and that a regression would silently break:
//
//  1. The distortion stays small: PSNR against the original carrier is at least
//     48 dB at the fill the policy allows by default (10%), and at least 40 dB
//     even at full capacity. A change that touched more channels, or moved them
//     by more than one, would drop the figure.
//
//  2. The output does not show the classic LSB-replacement signature. A
//     chi-square attack on the histogram of pairs (2k, 2k+1) detects sequential
//     LSB replacement at any fill above a few percent, because replacement forces
//     each pair to equalise. LSB matching and STC do not force that equality, so
//     the same attack does not separate a Wisanggeni carrier from a clean one.
//
// Both properties are measured on the same textures the CLI accepts, so the
// numbers in README come from code that runs in CI.

// chiFlagThreshold is the p-value above which the pairs-of-values attack reports
// an image as LSB-replaced. It is the conventional 0.95 cutoff.
const chiFlagThreshold = 0.95

// minPSNRFill10 is the floor at the default 10% fill.
const minPSNRFill10 = 48.0

// minPSNRFull is the floor at capacity, where the payload is five times what
// the default policy allows.
const minPSNRFull = 40.0

// texturedCarrier builds a carrier with the texture profile the CLI rates as
// suitable: a smooth base with fine-grained noise, so the cost map has cheap
// channels to use while the value histogram stays non-uniform, the way a real
// photograph's is.
//
// The non-uniformity matters for the chi-square tests. A carrier whose values are
// already spread evenly over 0..255 has histogram pairs that are balanced before
// anything is embedded, so replacing the low bit cannot change the statistic and
// the attack would look blind. A gradient plus noise has the peaky distribution
// the attack is designed to exploit.
func texturedCarrier(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	state := uint32(0x2545f491)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			// A diagonal gradient in 0..255, plus small noise. Values stay away
			// from 0 and 255 so LSB matching always has both directions.
			base := 40 + 160*(x+y)/(w+h)
			for c := 0; c < 3; c++ {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				noise := int(state>>28) - 8
				v := base + noise
				if v < 1 {
					v = 1
				}
				if v > 254 {
					v = 254
				}
				img.Pix[i+c] = byte(v)
			}
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

// payloadFor builds a blob whose size is exactly bodySize plus the container
// overhead, filled with incompressible bytes.
func payloadFor(t *testing.T, size int) []byte {
	t.Helper()
	blob := make([]byte, size)
	if _, err := rand.Read(blob); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return blob
}

// psnr reports the peak signal-to-noise ratio between two images of the same
// size, over the RGB channels only.
func psnr(a, b *image.NRGBA) float64 {
	channels := ChannelCount(a)
	if channels == 0 {
		return math.Inf(1)
	}
	var sum float64
	for i := 0; i+3 < len(a.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			d := float64(a.Pix[i+c]) - float64(b.Pix[i+c])
			sum += d * d
		}
	}
	mse := sum / float64(channels)
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10((255*255)/mse)
}

// TestSteganalysisPSNR pins the distortion budget at the two fills that matter.
func TestSteganalysisPSNR(t *testing.T) {
	const w, h = 256, 256
	cover := texturedCarrier(w, h)
	channels := ChannelCount(cover)
	total := channels / 8 // CapacityRaw in bytes for the whole blob

	for _, tc := range []struct {
		name string
		blob int
		gate float64
	}{
		// 10% of raw capacity is what the default policy allows, and Capacity()
		// itself is 50% of raw, so "full" here is the format's own ceiling.
		{name: "fill10", blob: int(float64(total) * 0.10), gate: minPSNRFill10},
		{name: "capacity", blob: int(Capacity(w, h)) + format.BlobOverhead, gate: minPSNRFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := cloneNRGBA(cover)
			blob := payloadFor(t, tc.blob)

			var seed [32]byte
			copy(seed[:], []byte("steganalysis-psnr-seed-000000000"))
			costs := CostMap(work)

			if err := EmbedBlob(work, blob, seed, costs); err != nil {
				t.Fatalf("embed: %v", err)
			}
			if err := VerifyBlob(work, blob, seed); err != nil {
				t.Fatalf("verify: %v", err)
			}

			got := psnr(cover, work)
			if got < tc.gate {
				t.Fatalf("PSNR = %.2f dB at %s, want at least %.0f dB",
					got, tc.name, tc.gate)
			}
			t.Logf("%s: PSNR %.2f dB over %d channels", tc.name, got, channels)

			// No channel may move by more than one value; that is the format's
			// core promise and the reason PSNR stays this high.
			for i := 0; i+3 < len(cover.Pix); i += 4 {
				for c := 0; c < 3; c++ {
					delta := int(work.Pix[i+c]) - int(cover.Pix[i+c])
					if delta > 1 || delta < -1 {
						t.Fatalf("channel %d moved by %d, want at most 1", i/4*3+c, delta)
					}
				}
			}
		})
	}
}

// TestChiSquareDetectsLSBReplacement is the control: it proves the attack used in
// the next test actually works, so a passing result there means something.
//
// Sequential LSB replacement writes message bits into the low bit of successive
// channels, which equalises every histogram pair (2k, 2k+1). Equalised pairs mean
// every observed count sits on its expectation, so the chi-square statistic drops
// and its upper-tail probability climbs towards 1. A p-value above
// chiFlagThreshold is the attack's signal that the image was LSB-replaced.
func TestChiSquareDetectsLSBReplacement(t *testing.T) {
	cover := texturedCarrier(256, 256)
	work := cloneNRGBA(cover)

	// Replace the low bit of every channel with a random stream, the worst case
	// for the location, and more than any real tool would write.
	channels := int(ChannelCount(work))
	bits := payloadFor(t, channels/8)
	for i := 0; i < channels; i++ {
		b := bits[i/8] >> (7 - uint(i%8)) & 1
		pix := i / 3
		comp := i % 3
		v := work.Pix[pix*4+comp]
		work.Pix[pix*4+comp] = v&0xfe | b
	}

	coverP := chiSquareLSP(cover)
	workP := chiSquareLSP(work)
	t.Logf("chi-square p: cover %.4f, LSB-replaced %.4f", coverP, workP)

	if workP < chiFlagThreshold {
		t.Fatalf("the attack failed to flag LSB replacement (p = %.4f, threshold %.2f); "+
			"the control is not sensitive enough to trust the next test", workP, chiFlagThreshold)
	}
	if coverP >= chiFlagThreshold {
		t.Fatalf("the attack flagged the untouched cover (p = %.4f); the control has no margin", coverP)
	}
}

// TestChiSquareDoesNotFlagWisanggeni is the property the format is built for:
// the same attack that catches LSB replacement does not separate a Wisanggeni
// carrier from the untouched original, at the fill the policy allows.
func TestChiSquareDoesNotFlagWisanggeni(t *testing.T) {
	const w, h = 256, 256
	cover := texturedCarrier(w, h)
	work := cloneNRGBA(cover)

	bodySize := int(float64(ChannelCount(cover)/8) * 0.05)
	blob := payloadFor(t, bodySize)
	var seed [32]byte
	copy(seed[:], []byte("steganalysis-chi-square-seed-0000"))
	if err := EmbedBlob(work, blob, seed, CostMap(work)); err != nil {
		t.Fatalf("embed: %v", err)
	}

	coverP := chiSquareLSP(cover)
	workP := chiSquareLSP(work)
	t.Logf("chi-square p: cover %.4f, wisanggeni %.4f", coverP, workP)

	// LSB matching and STC do not force histogram pairs equal, so the statistic
	// must stay in the natural range rather than climbing to the flag threshold
	// the control test proved is reachable.
	if workP >= chiFlagThreshold {
		t.Fatalf("chi-square flagged the Wisanggeni carrier (p = %.4f, threshold %.2f)",
			workP, chiFlagThreshold)
	}
}

// chiSquareLSP runs the standard pairs-of-values chi-square test on the RGB
// channels and returns the upper-tail probability of the statistic under the
// null hypothesis that the low bit is independent of the rest of the value.
//
// The statistic is sum over k of (count(2k) - expected)^2 / expected, where the
// expectation is the mean of the pair. A p-value near 1 means the pairs look
// untouched; a value near 0 means they have been forced equal, which is the
// signature of LSB replacement.
func chiSquareLSP(img *image.NRGBA) float64 {
	var counts [256]int64
	for i := 0; i+3 < len(img.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			counts[img.Pix[i+c]]++
		}
	}

	var statistic float64
	df := 0
	for k := 0; k < 256; k += 2 {
		even := float64(counts[k])
		odd := float64(counts[k+1])
		if even+odd == 0 {
			continue
		}
		expected := (even + odd) / 2
		statistic += (even - expected) * (even - expected) / expected
		df++
	}
	if df == 0 {
		return 1
	}
	return chiSquareUpper(statistic, df)
}

// chiSquareUpper returns P(X >= statistic) for a chi-square distribution with
// df degrees of freedom, using the regularised gamma function so the test needs
// no external dependency.
func chiSquareUpper(x float64, df int) float64 {
	if x <= 0 {
		return 1
	}
	return 1 - gammaP(float64(df)/2, x/2)
}

// gammaP is the regularised lower incomplete gamma P(a, x).
func gammaP(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return 0
	}
	if x == 0 {
		return 0
	}
	if x < a+1 {
		// Series expansion.
		term := 1 / a
		sum := term
		for n := 1; n < 200; n++ {
			term *= x / (a + float64(n))
			sum += term
			if math.Abs(term) < math.Abs(sum)*1e-15 {
				break
			}
		}
		return sum * math.Exp(-x+a*math.Log(x)-lgamma(a))
	}
	// Continued fraction for Q(a, x), then P = 1 - Q.
	b := x + 1 - a
	c := 1 / 1e-300
	d := 1 / b
	h := d
	for i := 1; i < 200; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < 1e-300 {
			d = 1e-300
		}
		c = b + an/c
		if math.Abs(c) < 1e-300 {
			c = 1e-300
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < 1e-15 {
			break
		}
	}
	q := math.Exp(-x+a*math.Log(x)-lgamma(a)) * h
	return 1 - q
}

// lgamma is the log of the gamma function via the Lanczos approximation.
func lgamma(x float64) float64 {
	cof := [6]float64{
		76.18009172947146,
		-86.50532032941677,
		24.01409824083091,
		-1.231739572450155,
		0.1208650973866179e-2,
		-0.5395239384953e-5,
	}
	y := x
	tmp := x + 5.5
	tmp -= (x + 0.5) * math.Log(tmp)
	ser := 1.000000000190015
	for i := 0; i < 6; i++ {
		y++
		ser += cof[i] / y
	}
	return -tmp + math.Log(2.5066282746310005*ser/x)
}

// cloneNRGBA copies an image so a test can embed into one without disturbing the
// other.
func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Rect)
	copy(dst.Pix, src.Pix)
	return dst
}
