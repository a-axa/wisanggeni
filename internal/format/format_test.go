package format_test

import (
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
)

func TestBlobLayout(t *testing.T) {
	checks := []struct {
		name string
		got  int
		want int
	}{
		{"SaltSize", format.SaltSize, 16},
		{"NonceSize", format.NonceSize, 24},
		{"TagSize", format.TagSize, 16},
		{"HeaderPlainSize", format.HeaderPlainSize, 16},

		{"HeaderNonceOffset", format.HeaderNonceOffset, 16},
		{"HeaderCipherOffset", format.HeaderCipherOffset, 40},
		{"HeaderCipherSize", format.HeaderCipherSize, 32},
		{"BodyNonceOffset", format.BodyNonceOffset, 72},
		{"BodyCipherOffset", format.BodyCipherOffset, 96},
		{"BlobOverhead", format.BlobOverhead, 112},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}

	// The layout must stay gap-free: every offset is the previous one plus its
	// size.
	if format.HeaderNonceOffset != format.SaltOffset+format.SaltSize {
		t.Error("salt and header nonce are not adjacent")
	}
	if format.BodyNonceOffset != format.HeaderCipherOffset+format.HeaderCipherSize {
		t.Error("header cipher and body nonce are not adjacent")
	}
	if format.BlobOverhead != format.BodyCipherOffset+format.TagSize {
		t.Error("body cipher and body tag are not adjacent")
	}
}

func TestHeaderPlainLayout(t *testing.T) {
	if format.HeaderVersionOffset != 0 || format.HeaderFlagsOffset != 1 {
		t.Fatalf("version/flags offsets = %d/%d, want 0/1",
			format.HeaderVersionOffset, format.HeaderFlagsOffset)
	}
	if format.HeaderReservedOffset+format.HeaderReservedSize != format.HeaderBodyLenOffset {
		t.Fatalf("reserved field is not adjacent to body_len")
	}
	if format.HeaderBodyLenOffset != 8 || format.HeaderBodyLenSize != 8 {
		t.Fatalf("body_len at %d size %d, want 8 and 8",
			format.HeaderBodyLenOffset, format.HeaderBodyLenSize)
	}
	if format.HeaderBodyLenOffset+format.HeaderBodyLenSize != format.HeaderPlainSize {
		t.Fatalf("header_plain is %d bytes, want %d",
			format.HeaderBodyLenOffset+format.HeaderBodyLenSize, format.HeaderPlainSize)
	}
}

func TestFrozenLabels(t *testing.T) {
	want := map[string]string{
		"LabelSaltPos": format.LabelSaltPos,
		"LabelSTC":     format.LabelSTC,
		"InfoEncKey":   format.InfoEncKey,
		"InfoPosSeed":  format.InfoPosSeed,
	}
	expect := map[string]string{
		"LabelSaltPos": "wisanggeni/v1/salt-pos",
		"LabelSTC":     "wisanggeni/v1/stc",
		"InfoEncKey":   "wisanggeni/v1/enc",
		"InfoPosSeed":  "wisanggeni/v1/pos",
	}
	for name, got := range want {
		if got != expect[name] {
			t.Errorf("%s = %q, want %q", name, got, expect[name])
		}
	}
}

func TestKDFParameters(t *testing.T) {
	if format.Argon2Time != 3 {
		t.Errorf("Argon2Time = %d, want 3", format.Argon2Time)
	}
	if format.Argon2MemoryKiB != 65536 {
		t.Errorf("Argon2MemoryKiB = %d, want 65536", format.Argon2MemoryKiB)
	}
	if format.Argon2Threads != 4 {
		t.Errorf("Argon2Threads = %d, want 4", format.Argon2Threads)
	}
	if format.Argon2KeyLen != 32 || format.HKDFHashLen != 32 {
		t.Errorf("key lengths = %d/%d, want 32/32", format.Argon2KeyLen, format.HKDFHashLen)
	}
	if format.Version != 1 {
		t.Errorf("Version = %d, want 1", format.Version)
	}
	if format.FlagZstd != 1 {
		t.Errorf("FlagZstd = %d, want 1", format.FlagZstd)
	}
}

func TestPNGChunkPolicy(t *testing.T) {
	preserved := make(map[string]bool, len(format.PreservedChunks))
	for _, name := range format.PreservedChunks {
		preserved[name] = true
	}
	for _, name := range format.DroppedChunks {
		if preserved[name] {
			t.Errorf("chunk %q is both preserved and dropped", name)
		}
	}

	wantPreserved := []string{"gAMA", "cHRM", "sRGB", "iCCP", "pHYs"}
	if len(format.PreservedChunks) != len(wantPreserved) {
		t.Fatalf("PreservedChunks = %v, want %v", format.PreservedChunks, wantPreserved)
	}
	for i, want := range wantPreserved {
		if format.PreservedChunks[i] != want {
			t.Errorf("PreservedChunks[%d] = %q, want %q", i, format.PreservedChunks[i], want)
		}
	}

	wantDropped := []string{"eXIf", "tEXt", "zTXt", "iTXt", "tIME"}
	if len(format.DroppedChunks) != len(wantDropped) {
		t.Fatalf("DroppedChunks = %v, want %v", format.DroppedChunks, wantDropped)
	}
	for i, want := range wantDropped {
		if format.DroppedChunks[i] != want {
			t.Errorf("DroppedChunks[%d] = %q, want %q", i, format.DroppedChunks[i], want)
		}
	}
}
