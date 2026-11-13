package crypto_test

import (
	"errors"
	"testing"

	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
)

// The KDF is intentionally expensive (Argon2id, 64 MiB), so these tests derive
// only the keys they compare rather than one per assertion.

const passphrase = "correct horse battery staple"

func testSalt(tag byte) []byte {
	salt := make([]byte, format.SaltSize)
	for i := range salt {
		salt[i] = tag + byte(i)
	}
	return salt
}

func TestDeriveDeterministicAndSaltSensitive(t *testing.T) {
	saltA, saltB := testSalt(1), testSalt(2)

	first, err := crypto.Derive(passphrase, saltA)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	second, err := crypto.Derive(passphrase, saltA)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if first != second {
		t.Error("same passphrase and salt produced different keys")
	}

	other, err := crypto.Derive(passphrase, saltB)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if first == other {
		t.Error("different salts produced the same keys")
	}

	if first.EncKey == first.PosSeed {
		t.Error("enc_key and pos_seed are identical; the info labels are not separating them")
	}
}

func TestDeriveNormalisesPassphrase(t *testing.T) {
	salt := testSalt(3)

	nfc, err := crypto.Derive("Caf\u00e9", salt)
	if err != nil {
		t.Fatalf("Derive NFC: %v", err)
	}
	nfd, err := crypto.Derive("Cafe\u0301", salt)
	if err != nil {
		t.Fatalf("Derive NFD: %v", err)
	}
	if nfc != nfd {
		t.Error("NFC and NFD spellings of the same passphrase derived different keys")
	}

	wide, err := crypto.Derive("\uff57\uff49\uff53\uff41\uff4e\uff47\uff47\uff45\uff4e\uff49", salt)
	if err != nil {
		t.Fatalf("Derive fullwidth: %v", err)
	}
	ascii, err := crypto.Derive("wisanggeni", salt)
	if err != nil {
		t.Fatalf("Derive ascii: %v", err)
	}
	if wide != ascii {
		t.Error("fullwidth and ASCII spellings of the same passphrase derived different keys")
	}

	if nfc == ascii {
		t.Error("two different passphrases derived the same keys")
	}
}

func TestDeriveRejectsEmptySalt(t *testing.T) {
	if _, err := crypto.Derive(passphrase, nil); !errors.Is(err, crypto.ErrEmptySalt) {
		t.Errorf("Derive with no salt = %v, want ErrEmptySalt", err)
	}
}

// BenchmarkDerive reports the per-call cost of the frozen parameters. §6.1
// expects roughly 0.5-1.5 s on a desktop; it is a design target, not a test
// assertion, because CI hardware varies.
func BenchmarkDerive(b *testing.B) {
	salt := testSalt(9)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := crypto.Derive(passphrase, salt); err != nil {
			b.Fatalf("Derive: %v", err)
		}
	}
}
