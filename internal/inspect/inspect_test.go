package inspect

import (
	"crypto/rand"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
	"github.com/a-axa/wisanggeni/internal/carrier"
	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/exitcode"
	"github.com/a-axa/wisanggeni/internal/format"
	"github.com/a-axa/wisanggeni/internal/stego"
)

// inspectNoise builds a textured carrier, which CoverScore rates as suitable.
// The name is unique to this package: cmd and internal/hide carry their own
// noiseImage helper, and the packages are compiled separately but the names
// still collide in a reader's head.
func inspectNoise(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	state := uint32(0x9e3779b9)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				img.Pix[i+c] = byte(state)
			}
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

// inspectPack arranges files under a folder and packs it, exactly as hide does.
func inspectPack(t *testing.T, files map[string][]byte, opts archive.Options) archive.Packed {
	t.Helper()
	root := filepath.Join(t.TempDir(), "payload")
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
	}
	packed, err := archive.Pack([]string{root}, opts)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return packed
}

// inspectHide seals a packed payload with flags and embeds it. flags is separate
// from the pack so a test can write a header this reader must refuse.
func inspectHide(t *testing.T, pass string, flags uint8, plain []byte) *image.NRGBA {
	t.Helper()
	img := inspectNoise(256, 256)

	salt := make([]byte, format.SaltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("rand salt: %v", err)
	}
	keys, err := crypto.Derive(pass, salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	blob, err := crypto.Seal(keys.EncKey, salt, flags, plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := stego.EmbedBlob(img, blob, keys.PosSeed, stego.CostMap(img)); err != nil {
		t.Fatalf("embed: %v", err)
	}
	return img
}

// packFlags is the header flag a Pack run implies.
func packFlags(packed archive.Packed) uint8 {
	if packed.Compressed {
		return format.FlagZstd
	}
	return 0
}

// TestDescribeReportsCarrierLayer pins that every number Describe reports comes
// straight from the frozen capacity table (§6.1) for the same dimensions.
func TestDescribeReportsCarrierLayer(t *testing.T) {
	const w, h = 40, 30
	img := inspectNoise(w, h)
	car := &carrier.Carrier{Image: img}

	info := Describe(car, true)

	if info.Width != w || info.Height != h {
		t.Fatalf("dimensions = %dx%d, want %dx%d", info.Width, info.Height, w, h)
	}
	if want := stego.Channels(w, h); info.Channels != want {
		t.Errorf("channels = %d, want %d", info.Channels, want)
	}
	if want := stego.CapacityRaw(w, h); info.Raw != want {
		t.Errorf("raw = %d, want %d", info.Raw, want)
	}
	if want := stego.Capacity(w, h); info.Usable != want {
		t.Errorf("usable = %d, want %d", info.Usable, want)
	}
	if want := stego.SuggestedCapacity(w, h); info.Suggested != want {
		t.Errorf("suggested = %d, want %d", info.Suggested, want)
	}
	if want := stego.StrongCapacity(w, h); info.Strong != want {
		t.Errorf("strong = %d, want %d", info.Strong, want)
	}

	if info.Alpha {
		t.Error("alpha = true for an opaque carrier")
	}
	if !info.Scored {
		t.Fatal("Scored = false, want true when the caller asks for the score")
	}
	if want := stego.CoverScore(img); info.Score != want {
		t.Errorf("score = %+v, want %+v", info.Score, want)
	}
}

// TestDescribeScoreOptional pins the contract that lets a capacity-only caller
// skip an O(pixels) pass: no score is measured unless asked for.
func TestDescribeScoreOptional(t *testing.T) {
	car := &carrier.Carrier{Image: inspectNoise(32, 32)}

	info := Describe(car, false)
	if info.Scored {
		t.Error("Scored = true, but the caller did not ask for a score")
	}
	if (info.Score != stego.CoverStats{}) {
		t.Errorf("Score = %+v, want the zero value", info.Score)
	}

	scored := Describe(car, true)
	if !scored.Scored {
		t.Error("Scored = false after asking for the score")
	}
	// The optional pass must not change the carrier numbers.
	if scored.Usable != info.Usable || scored.Channels != info.Channels {
		t.Errorf("score pass changed the capacity numbers: %+v vs %+v", scored, info)
	}
}

// TestDescribeCopiesChunksAndWarnings keeps the report a snapshot: a caller that
// mutates the carrier afterwards must not rewrite what was reported.
func TestDescribeCopiesChunksAndWarnings(t *testing.T) {
	car := &carrier.Carrier{
		Image:    inspectNoise(16, 16),
		Chunks:   []carrier.Chunk{{Type: "gAMA"}, {Type: "sRGB"}},
		Warnings: []string{"colour chunk present"},
	}

	info := Describe(car, false)

	if want := []string{"gAMA", "sRGB"}; !reflect.DeepEqual(info.Chunks, want) {
		t.Errorf("chunks = %v, want %v", info.Chunks, want)
	}
	if want := []string{"colour chunk present"}; !reflect.DeepEqual(info.Warnings, want) {
		t.Errorf("warnings = %v, want %v", info.Warnings, want)
	}

	car.Warnings[0] = "mutated"
	if info.Warnings[0] != "colour chunk present" {
		t.Error("Warnings aliases the carrier's slice")
	}
}

// TestDescribeAlphaCarrier forbids pretending to score a carrier the engine
// refuses to embed in.
func TestDescribeAlphaCarrier(t *testing.T) {
	car := &carrier.Carrier{Image: inspectNoise(16, 16), Alpha: true}

	info := Describe(car, false)
	if !info.Alpha {
		t.Error("alpha = false for a transparent carrier")
	}
	if info.Scored {
		t.Error("an alpha carrier was scored")
	}
}

// TestOpenPayloadListsArchive pins that the payload layer is the archive layer:
// the entries, file count and total must match archive.List of the same bytes,
// and nothing is extracted.
func TestOpenPayloadListsArchive(t *testing.T) {
	pass := "correct horse battery staple"
	for _, tc := range []struct {
		name string
		opts archive.Options
	}{
		{name: "zstd"},
		{name: "no-compress", opts: archive.Options{NoCompress: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packed := inspectPack(t, map[string][]byte{
				"a.txt":        []byte("alpha"),
				"nested/b.txt": []byte("beta gamma"),
			}, tc.opts)
			img := inspectHide(t, pass, packFlags(packed), packed.Data)

			payload, err := OpenPayload(img, []byte(pass), false)
			if err != nil {
				t.Fatalf("OpenPayload: %v", err)
			}

			want, err := archive.List(packed.Data, archive.ListOptions{Compressed: packed.Compressed})
			if err != nil {
				t.Fatalf("archive.List: %v", err)
			}
			if !reflect.DeepEqual(payload.Entries, want) {
				t.Fatalf("entries = %+v, want %+v", payload.Entries, want)
			}

			var files int
			var total int64
			for _, entry := range want {
				if !entry.Dir {
					files++
					total += entry.Size
				}
			}
			if payload.Files != files {
				t.Errorf("files = %d, want %d", payload.Files, files)
			}
			if payload.Total != total {
				t.Errorf("total = %d, want %d", payload.Total, total)
			}

			if payload.Version != format.Version {
				t.Errorf("version = %d, want %d", payload.Version, format.Version)
			}
			if payload.Compressed != packed.Compressed {
				t.Errorf("compressed = %v, want %v", payload.Compressed, packed.Compressed)
			}
			if payload.BodyLen != uint64(len(packed.Data)) {
				t.Errorf("body = %d, want %d", payload.BodyLen, len(packed.Data))
			}
			if payload.HeaderOnly {
				t.Error("HeaderOnly = true after a full open")
			}
		})
	}
}

// TestOpenPayloadHeaderOnly pins the shallow depth: the header is reported and
// the archive is never parsed, so entries stay empty by request.
func TestOpenPayloadHeaderOnly(t *testing.T) {
	pass := "correct horse battery staple"
	packed := inspectPack(t, map[string][]byte{"a.txt": []byte("alpha")}, archive.Options{})
	img := inspectHide(t, pass, packFlags(packed), packed.Data)

	payload, err := OpenPayload(img, []byte(pass), true)
	if err != nil {
		t.Fatalf("OpenPayload: %v", err)
	}
	if !payload.HeaderOnly {
		t.Error("HeaderOnly = false, want true")
	}
	if len(payload.Entries) != 0 {
		t.Errorf("entries = %+v, want none in header-only mode", payload.Entries)
	}
	if payload.Files != 0 || payload.Total != 0 {
		t.Errorf("files/total = %d/%d, want 0/0", payload.Files, payload.Total)
	}
	if payload.BodyLen != uint64(len(packed.Data)) {
		t.Errorf("body = %d, want %d", payload.BodyLen, len(packed.Data))
	}
}

// TestOpenPayloadWrongPassphrase keeps inspect from becoming an oracle: the same
// opaque decrypt error as reveal, exit 3.
func TestOpenPayloadWrongPassphrase(t *testing.T) {
	packed := inspectPack(t, map[string][]byte{"a.txt": []byte("alpha")}, archive.Options{})
	img := inspectHide(t, "correct horse battery staple", packFlags(packed), packed.Data)

	_, err := OpenPayload(img, []byte("wrong passphrase entirely"), false)
	if err == nil {
		t.Fatal("OpenPayload accepted the wrong passphrase")
	}
	if code := exitcode.Code(err); code != exitcode.Decrypt {
		t.Errorf("exit = %d, want %d", code, exitcode.Decrypt)
	}
	if !strings.Contains(err.Error(), "cannot decrypt") {
		t.Errorf("error %q is not the generic decrypt message", err)
	}
}

// TestOpenPayloadPlainImage covers a carrier with no blob at all.
func TestOpenPayloadPlainImage(t *testing.T) {
	img := inspectNoise(256, 256)

	_, err := OpenPayload(img, []byte("correct horse battery staple"), false)
	if err == nil {
		t.Fatal("OpenPayload found a payload in a plain image")
	}
	if code := exitcode.Code(err); code != exitcode.Decrypt {
		t.Fatalf("exit = %d (%v), want %d", code, err, exitcode.Decrypt)
	}
}

// TestOpenPayloadIncompatibleFlags covers an authenticated header this reader
// must refuse. The flags byte is the one field Seal lets a test set, and an
// unknown bit is exactly what a future v1 writer would trip.
func TestOpenPayloadIncompatibleFlags(t *testing.T) {
	img := inspectHide(t, "correct horse battery staple", 0x02, []byte("raw tar bytes"))

	_, err := OpenPayload(img, []byte("correct horse battery staple"), false)
	if err == nil {
		t.Fatal("OpenPayload accepted an unknown flag")
	}
	if code := exitcode.Code(err); code != exitcode.Decrypt {
		t.Errorf("exit = %d, want %d", code, exitcode.Decrypt)
	}
	if !strings.Contains(err.Error(), "incompatible writer") {
		t.Errorf("error %q does not name the incompatible writer", err)
	}
}
