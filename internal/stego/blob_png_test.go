package stego_test

import (
	"bytes"
	"math/rand/v2"
	"path/filepath"
	"testing"

	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// TestBlobThroughPNG exercises §7.8 steps 10-14 without crypto: embed the blob,
// write the carrier through the chunk-preserving writer, reload it through the
// normalising reader, and verify the blob. This is the path the writer's
// self-check takes before it renames the temporary file into place.
func TestBlobThroughPNG(t *testing.T) {
	rng := rand.New(rand.NewPCG(61, 62))
	img := randomNRGBA(rng, 96, 64)
	seed := testPosSeed(5)

	blob := make([]byte, stego.CapacityRaw(96, 64)*9/10)
	for i := range blob {
		blob[i] = byte(rng.IntN(256))
	}

	costs := stego.CostMap(img)
	if err := stego.EmbedBlob(img, blob, seed, costs); err != nil {
		t.Fatalf("EmbedBlob: %v", err)
	}

	path := filepath.Join(t.TempDir(), "stego.png")
	if err := carrier.Save(path, img, nil); err != nil {
		t.Fatalf("carrier.Save: %v", err)
	}

	reloaded, err := carrier.Load(path)
	if err != nil {
		t.Fatalf("carrier.Load: %v", err)
	}

	if !bytes.Equal(reloaded.Image.Pix, img.Pix) {
		t.Fatal("PNG round trip changed the pixels")
	}
	if err := stego.VerifyBlob(reloaded.Image, blob, seed); err != nil {
		t.Fatalf("VerifyBlob after PNG round trip: %v", err)
	}
}
