// Package crypto implements key derivation and the container AEAD for container
// format v1 (§6, §6.1).
//
// The parameters here are frozen: a reader with the wrong Argon2 cost, HKDF
// label or normalisation form cannot decrypt anything, and there is no plaintext
// header to negotiate with.
package crypto

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/text/unicode/norm"

	"github.com/a-axa/wisanggeni/internal/format"
)

// ErrEmptySalt reports a derivation with no salt. The format always stores a
// random 16-byte salt (§6); deriving without one would make the same passphrase
// produce the same key for every file.
var ErrEmptySalt = errors.New("crypto: salt is empty")

// Keys are the two secrets derived from a passphrase: the AEAD key that seals
// the container, and the seed that drives the keyed STC pool order (§7.2).
type Keys struct {
	EncKey  [format.HKDFHashLen]byte
	PosSeed [format.HKDFHashLen]byte
}

// Derive turns a passphrase and a salt into the container keys (§6.1).
//
// Normalisation lives here, not at the call site, so every path that reaches a
// key has normalised identically: NFKC first, then the UTF-8 bytes into
// Argon2id. The Argon2id salt is not reused: HKDF runs with a nil salt and two
// separate Expand calls with distinct info labels, so enc_key and pos_seed are
// independent outputs rather than halves of one longer Expand.
//
// The cost is deliberately high (t=3, 64 MiB, p=4): roughly 0.5-1.5 s per call
// on a desktop, which is the intended brute-force barrier.
func Derive(passphrase string, salt []byte) (Keys, error) {
	if len(salt) == 0 {
		return Keys{}, ErrEmptySalt
	}

	k := argon2.IDKey([]byte(norm.NFKC.String(passphrase)), salt,
		format.Argon2Time, format.Argon2MemoryKiB, format.Argon2Threads, format.Argon2KeyLen)
	// The intermediate K is no longer needed once both keys are expanded.
	defer clear(k)

	var keys Keys
	if err := expandInto(k, format.InfoEncKey, keys.EncKey[:]); err != nil {
		return Keys{}, err
	}
	if err := expandInto(k, format.InfoPosSeed, keys.PosSeed[:]); err != nil {
		return Keys{}, err
	}
	return keys, nil
}

// expandInto fills dst with HKDF-Expand(SHA-256) using PRK = k, salt nil and the
// given info string (§6.1). dst is at most 32 bytes, far below the 255*HashLen
// HKDF limit, so a short read is a defect rather than bad input.
func expandInto(k []byte, info string, dst []byte) error {
	if _, err := io.ReadFull(hkdf.New(sha256.New, k, nil, []byte(info)), dst); err != nil {
		return fmt.Errorf("crypto: hkdf expand %q: %w", info, err)
	}
	return nil
}
