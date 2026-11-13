package crypto_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/a-axa/wisanggeni/internal/crypto"
	"github.com/a-axa/wisanggeni/internal/format"
)

func testKey(tag byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = tag + byte(i*3)
	}
	return key
}

func randomPlain(rng *rand.Rand, n int) []byte {
	plain := make([]byte, n)
	for i := range plain {
		plain[i] = byte(rng.IntN(256))
	}
	return plain
}

func TestSealOpenRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(71, 72))
	encKey := testKey(1)
	salt := testSalt(4)

	for _, size := range []int{0, 1, 16, 4096, 65536} {
		for _, flags := range []uint8{0, format.FlagZstd} {
			plain := randomPlain(rng, size)

			blob, err := crypto.Seal(encKey, salt, flags, plain)
			if err != nil {
				t.Fatalf("size %d flags %d: Seal: %v", size, flags, err)
			}
			if want := format.BlobOverhead + size; len(blob) != want {
				t.Fatalf("size %d: blob length = %d, want %d", size, len(blob), want)
			}

			header, body, err := crypto.Open(encKey, blob, uint64(size))
			if err != nil {
				t.Fatalf("size %d flags %d: Open: %v", size, flags, err)
			}
			if header.Version != format.Version || header.Flags != flags || header.BodyLen != uint64(size) {
				t.Errorf("size %d flags %d: header = %+v", size, flags, header)
			}
			if !bytes.Equal(body, plain) {
				t.Errorf("size %d flags %d: body round trip mismatch", size, flags)
			}
		}
	}
}

func TestSealRejectsBadSalt(t *testing.T) {
	encKey := testKey(2)
	for _, salt := range [][]byte{nil, make([]byte, format.SaltSize-1), make([]byte, format.SaltSize+1)} {
		if _, err := crypto.Seal(encKey, salt, 0, []byte("x")); !errors.Is(err, crypto.ErrSaltSize) {
			t.Errorf("Seal with a %d-byte salt = %v, want ErrSaltSize", len(salt), err)
		}
	}
}

func TestOpenDetectsTamper(t *testing.T) {
	encKey := testKey(3)
	salt := testSalt(5)
	plain := []byte("dawn")

	blob, err := crypto.Seal(encKey, salt, format.FlagZstd, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// §6: the body is bound to header_ct, which is bound to the salt, so every
	// single byte of the blob is authenticated. Flip one bit in each byte.
	for off := range blob {
		tampered := append([]byte(nil), blob...)
		tampered[off] ^= 0x01

		if _, _, err := crypto.Open(encKey, tampered, uint64(len(plain))); !errors.Is(err, crypto.ErrDecrypt) {
			t.Fatalf("flipping the low bit at offset %d (%s) = %v, want ErrDecrypt",
				off, regionOf(off), err)
		}
	}
}

// regionOf names the §6 field containing an offset, for test diagnostics.
func regionOf(off int) string {
	switch {
	case off < format.HeaderNonceOffset:
		return "salt"
	case off < format.HeaderCipherOffset:
		return "header_nonce"
	case off < format.BodyNonceOffset:
		return "header_ct"
	case off < format.BodyCipherOffset:
		return "body_nonce"
	default:
		return "body_ct"
	}
}

func TestOpenWrongKeyIsIndistinguishable(t *testing.T) {
	encKey := testKey(6)
	plain := []byte("the same message every wrong key sees")

	blob, err := crypto.Seal(encKey, testSalt(7), 0, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	_, _, errWrongKey := crypto.Open(testKey(8), blob, uint64(len(plain)))
	if !errors.Is(errWrongKey, crypto.ErrDecrypt) {
		t.Fatalf("Open with the wrong key = %v, want ErrDecrypt", errWrongKey)
	}

	garbage := randomPlain(rand.New(rand.NewPCG(9, 10)), len(blob))
	_, _, errGarbage := crypto.Open(encKey, garbage, uint64(len(plain)))
	if !errors.Is(errGarbage, crypto.ErrDecrypt) {
		t.Fatalf("Open with random data = %v, want ErrDecrypt", errGarbage)
	}

	if !errors.Is(errWrongKey, errGarbage) {
		t.Errorf("a wrong key and random data gave %v and %v; they must be identical", errWrongKey, errGarbage)
	}
}

func TestOpenRejectsShortBlob(t *testing.T) {
	if _, _, err := crypto.Open(testKey(1), make([]byte, format.BlobOverhead-1), 0); !errors.Is(err, crypto.ErrDecrypt) {
		t.Errorf("Open with a short blob = %v, want ErrDecrypt", err)
	}
}

// TestOpenHeaderAndBodySplit drives the reveal path: read the header from the
// salt and the 80-byte prefix, then the body from its own nonce and the header
// ciphertext, without ever holding the whole blob.
func TestOpenHeaderAndBodySplit(t *testing.T) {
	encKey := testKey(4)
	salt := testSalt(6)
	plain := randomPlain(rand.New(rand.NewPCG(11, 12)), 300)

	blob, err := crypto.Seal(encKey, salt, format.FlagZstd, plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	headerCT := blob[format.HeaderCipherOffset:format.BodyNonceOffset]

	header, err := crypto.OpenHeader(encKey,
		blob[format.SaltOffset:format.HeaderNonceOffset],
		blob[format.HeaderNonceOffset:format.HeaderCipherOffset],
		headerCT,
	)
	if err != nil {
		t.Fatalf("OpenHeader: %v", err)
	}
	if header.BodyLen != uint64(len(plain)) || header.Flags != format.FlagZstd {
		t.Errorf("header = %+v, want body_len %d and flags %d", header, len(plain), format.FlagZstd)
	}

	body, err := crypto.OpenBody(encKey,
		headerCT,
		blob[format.BodyNonceOffset:format.BodyCipherOffset],
		blob[format.BodyCipherOffset:],
	)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	if !bytes.Equal(body, plain) {
		t.Error("OpenHeader/OpenBody split did not reproduce the plaintext")
	}
}
