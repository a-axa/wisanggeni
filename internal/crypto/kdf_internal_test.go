package crypto

import (
	"encoding/hex"
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
)

// TestExpandMatchesFormatVectors pins the HKDF half of §6.1 independently of
// Argon2id: with K = 00..1f and a nil HKDF salt, the two Expand calls must
// reproduce the frozen vectors. A drift here makes every v1 file undecryptable.
func TestExpandMatchesFormatVectors(t *testing.T) {
	k := make([]byte, format.Argon2KeyLen)
	for i := range k {
		k[i] = byte(i)
	}

	cases := []struct {
		info string
		want string
	}{
		{format.InfoEncKey, "4f9e59b17124a88318dcb4f93f891901ae7b5b8758410e331ed314530609914a"},
		{format.InfoPosSeed, "e4efae5a801d64e3cdc9fe0bd2b1c9041e3841a4e96f790ad38a6d9ebc2f4c19"},
	}

	for _, c := range cases {
		got := make([]byte, format.HKDFHashLen)
		if err := expandInto(k, c.info, got); err != nil {
			t.Fatalf("%s: expandInto: %v", c.info, err)
		}
		if hex.EncodeToString(got) != c.want {
			t.Errorf("%s = %s, want %s", c.info, hex.EncodeToString(got), c.want)
		}
	}
}
