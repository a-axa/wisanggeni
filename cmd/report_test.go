package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/stego"
)

func runCapacityCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := newCapacityCmd()
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	c.SilenceErrors = true
	c.SilenceUsage = true
	err = c.Execute()
	return out.String(), errOut.String(), err
}

func runScoreCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	c := newScoreCmd()
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	c.SilenceErrors = true
	c.SilenceUsage = true
	err = c.Execute()
	return out.String(), errOut.String(), err
}

// writeAlphaPNG saves an otherwise ordinary image with one translucent pixel.
func writeAlphaPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 200, 100, 50, 0xff
	}
	img.Pix[3] = 0x80

	path := filepath.Join(t.TempDir(), "alpha.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create alpha PNG: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		t.Fatalf("encode alpha PNG: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close alpha PNG: %v", err)
	}
	return path
}

func TestCapacityReport(t *testing.T) {
	path := saveImage(t, noiseImage(256, 256))

	stdout, stderr, err := runCapacityCLI(t, path)
	if err != nil {
		t.Fatalf("capacity: %v (stderr %s)", err, stderr)
	}

	want := []string{
		"dimensions    256x256",
		fmt.Sprintf("channels      %d", stego.Channels(256, 256)),
		fmt.Sprintf("capacity raw  %d bytes", stego.CapacityRaw(256, 256)),
		fmt.Sprintf("usable        %d bytes", stego.Capacity(256, 256)),
		fmt.Sprintf("suggested     %d bytes (10%% fill)", stego.SuggestedCapacity(256, 256)),
		fmt.Sprintf("strong        %d bytes (5%% fill)", stego.StrongCapacity(256, 256)),
		"cover         suitable",
		"alpha         no",
		"chunks        none",
	}
	for _, w := range want {
		if !strings.Contains(stdout, w) {
			t.Errorf("capacity output lacks %q:\n%s", w, stdout)
		}
	}
}

func TestCapacityReportsChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chunked.png")
	chunks := []carrier.Chunk{{Type: "gAMA", Data: []byte{0, 0, 0, 1}}}
	if err := carrier.Save(path, noiseImage(64, 64), chunks); err != nil {
		t.Fatalf("save with chunk: %v", err)
	}

	stdout, stderr, err := runCapacityCLI(t, path)
	if err != nil {
		t.Fatalf("capacity: %v (stderr %s)", err, stderr)
	}
	if !strings.Contains(stdout, "chunks        gAMA") {
		t.Errorf("capacity output does not name the chunk:\n%s", stdout)
	}
	// A colour-management chunk also earns a warning on stderr.
	if !strings.Contains(stderr, "gAMA") {
		t.Errorf("stderr = %q, want a gAMA warning", stderr)
	}
}

func TestCapacityAlphaCarrier(t *testing.T) {
	path := writeAlphaPNG(t, 40, 30)

	// The engine still refuses it: only the reporting commands tolerate alpha.
	if _, err := carrier.Load(path); !errors.Is(err, carrier.ErrAlpha) {
		t.Fatalf("Load error = %v, want ErrAlpha", err)
	}

	stdout, stderr, err := runCapacityCLI(t, path)
	if err != nil {
		t.Fatalf("capacity on an alpha carrier: %v (stderr %s)", err, stderr)
	}
	for _, w := range []string{
		"dimensions    40x30",
		"cover         not scored",
		"alpha         yes",
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("capacity output lacks %q:\n%s", w, stdout)
		}
	}
}

func TestScoreVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  *image.NRGBA
		want string
	}{
		{name: "textured", img: noiseImage(64, 64), want: "suitable"},
		{name: "flat", img: flatImage(64, 64), want: "reject"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := saveImage(t, tc.img)

			stdout, stderr, err := runScoreCLI(t, path)
			if err != nil {
				t.Fatalf("score: %v (stderr %s)", err, stderr)
			}

			// Compare against the score of the saved file, so the numbers are the
			// ones the reader will see.
			car, err := carrier.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			stats := stego.CoverScore(car.Image)

			want := []string{
				fmt.Sprintf("textured fraction   %.3f", stats.TexturedFraction),
				fmt.Sprintf("median rho          %.6g", stats.MedianRho),
				fmt.Sprintf("verdict             %s", tc.want),
			}
			for _, w := range want {
				if !strings.Contains(stdout, w) {
					t.Errorf("score output lacks %q:\n%s", w, stdout)
				}
			}
		})
	}
}

func TestScoreAlphaCarrier(t *testing.T) {
	stdout, stderr, err := runScoreCLI(t, writeAlphaPNG(t, 40, 30))
	if err != nil {
		t.Fatalf("score on an alpha carrier: %v (stderr %s)", err, stderr)
	}
	if !strings.Contains(stdout, "the carrier has transparency") {
		t.Errorf("score output does not flag transparency:\n%s", stdout)
	}
}
