package format_test

import (
	"encoding/hex"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// TestNFKCVectors freezes the passphrase normalisation of §6.1. The KDF is fed
// NFKC output as UTF-8 bytes, so the same passphrase typed on a different
// platform (NFC vs NFD) must produce the same bytes.
func TestNFKCVectors(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantHex  string
		wantText string
	}{
		{
			name:     "nfc",
			input:    "Caf\u00e9",
			wantHex:  "436166c3a9",
			wantText: "Caf\u00e9",
		},
		{
			name:     "nfd",
			input:    "Cafe\u0301",
			wantHex:  "436166c3a9",
			wantText: "Caf\u00e9",
		},
		{
			name:     "fullwidth",
			input:    "\uff57\uff49\uff53\uff41\uff4e\uff47\uff47\uff45\uff4e\uff49",
			wantHex:  "776973616e6767656e69",
			wantText: "wisanggeni",
		},
	}

	for _, c := range cases {
		got := norm.NFKC.String(c.input)
		if got != c.wantText {
			t.Errorf("%s: NFKC = %q, want %q", c.name, got, c.wantText)
		}
		if hex.EncodeToString([]byte(got)) != c.wantHex {
			t.Errorf("%s: NFKC bytes = %s, want %s", c.name, hex.EncodeToString([]byte(got)), c.wantHex)
		}
	}

	// The two spellings of "café" must collapse to one passphrase.
	if norm.NFKC.String(cases[0].input) != norm.NFKC.String(cases[1].input) {
		t.Error("NFC and NFD forms normalise differently")
	}
}
