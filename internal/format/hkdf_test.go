package format_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/hkdf"

	"github.com/a-axa/wisanggeni/internal/format"
)

// TestHKDFVectors freezes the derivation of §6.1. The passphrase step is not
// involved: K is the fixed 32-byte Argon2id output below, the HKDF salt is nil,
// and the two Expand calls are separate with distinct info labels.
//
// These values are the contract between writer and reader. Changing the salt,
// the number of Expand calls, or an info label makes every v1 file unreadable.
func TestHKDFVectors(t *testing.T) {
	k := make([]byte, format.Argon2KeyLen)
	for i := range k {
		k[i] = byte(i)
	}

	cases := []struct {
		name string
		info string
		want string
	}{
		{
			name: "enc_key",
			info: format.InfoEncKey,
			want: "4f9e59b17124a88318dcb4f93f891901ae7b5b8758410e331ed314530609914a",
		},
		{
			name: "pos_seed",
			info: format.InfoPosSeed,
			want: "e4efae5a801d64e3cdc9fe0bd2b1c9041e3841a4e96f790ad38a6d9ebc2f4c19",
		},
	}

	for _, c := range cases {
		got := make([]byte, format.HKDFHashLen)
		if _, err := hkdf.New(sha256.New, k, nil, []byte(c.info)).Read(got); err != nil {
			t.Fatalf("%s: read: %v", c.name, err)
		}
		if hex.EncodeToString(got) != c.want {
			t.Errorf("%s = %s, want %s", c.name, hex.EncodeToString(got), c.want)
		}
	}

	// Two separate Expand calls must not be replaced by one 64-byte Expand
	// split in half (§11.9). The first 32 bytes of the long Expand do equal
	// enc_key; the second half must not equal pos_seed.
	long := make([]byte, 2*format.HKDFHashLen)
	if _, err := hkdf.New(sha256.New, k, nil, []byte(format.InfoEncKey)).Read(long); err != nil {
		t.Fatalf("read long expand: %v", err)
	}
	if hex.EncodeToString(long[:format.HKDFHashLen]) != cases[0].want {
		t.Error("first half of the long Expand differs from enc_key; the vectors disagree")
	}
	if hex.EncodeToString(long[format.HKDFHashLen:]) == cases[1].want {
		t.Error("second half of the long Expand equals pos_seed; the labels are not separating the keys")
	}
}
