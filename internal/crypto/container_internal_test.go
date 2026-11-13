package crypto

import (
	"encoding/binary"
	"errors"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/a-axa/wisanggeni/internal/format"
)

func fixedNonce(tag byte) []byte {
	nonce := make([]byte, format.NonceSize)
	for i := range nonce {
		nonce[i] = tag + byte(i*5)
	}
	return nonce
}

// intKey and intSalt mirror the black-box helpers; the internal test cannot see
// the crypto_test package's versions.
func intKey(tag byte) [32]byte {
	var key [32]byte
	for i := range key {
		key[i] = tag + byte(i*3)
	}
	return key
}

func intSalt(tag byte) []byte {
	salt := make([]byte, format.SaltSize)
	for i := range salt {
		salt[i] = tag + byte(i)
	}
	return salt
}

func headerPlainFor(flags uint8, bodyLen uint64) []byte {
	plain := make([]byte, format.HeaderPlainSize)
	plain[format.HeaderVersionOffset] = format.Version
	plain[format.HeaderFlagsOffset] = flags
	binary.LittleEndian.PutUint64(plain[format.HeaderBodyLenOffset:], bodyLen)
	return plain
}

// TestSealLayout pins the byte layout of §6 with fixed nonces, so a shifted
// offset is caught without relying on round-trip symmetry.
func TestSealLayout(t *testing.T) {
	encKey := intKey(11)
	salt := intSalt(12)
	headerNonce := fixedNonce(1)
	bodyNonce := fixedNonce(2)
	plain := []byte("body bytes")

	blob, err := seal(encKey, salt, headerPlainFor(format.FlagZstd, uint64(len(plain))),
		plain, headerNonce, bodyNonce)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if want := format.BlobOverhead + len(plain); len(blob) != want {
		t.Fatalf("blob length = %d, want %d", len(blob), want)
	}

	if got := blob[format.SaltOffset:format.HeaderNonceOffset]; string(got) != string(salt) {
		t.Errorf("salt bytes = %x, want %x", got, salt)
	}
	if got := blob[format.HeaderNonceOffset:format.HeaderCipherOffset]; string(got) != string(headerNonce) {
		t.Errorf("header nonce = %x, want %x", got, headerNonce)
	}
	if got := blob[format.BodyNonceOffset:format.BodyCipherOffset]; string(got) != string(bodyNonce) {
		t.Errorf("body nonce = %x, want %x", got, bodyNonce)
	}
	if got := len(blob[format.BodyCipherOffset:]); got != len(plain)+format.TagSize {
		t.Errorf("body_cipher length = %d, want %d", got, len(plain)+format.TagSize)
	}

	// header_plain must decrypt with the salt as AAD and carry the layout.
	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		t.Fatalf("NewX: %v", err)
	}
	headerPlain, err := aead.Open(nil, headerNonce, blob[format.HeaderCipherOffset:format.BodyNonceOffset], salt)
	if err != nil {
		t.Fatalf("open header: %v", err)
	}
	if headerPlain[format.HeaderVersionOffset] != format.Version {
		t.Errorf("version = %d, want %d", headerPlain[format.HeaderVersionOffset], format.Version)
	}
	if headerPlain[format.HeaderFlagsOffset] != format.FlagZstd {
		t.Errorf("flags = %d, want %d", headerPlain[format.HeaderFlagsOffset], format.FlagZstd)
	}
	for i := format.HeaderReservedOffset; i < format.HeaderBodyLenOffset; i++ {
		if headerPlain[i] != 0 {
			t.Errorf("reserved byte %d = %d, want 0", i, headerPlain[i])
		}
	}
	if got := binary.LittleEndian.Uint64(headerPlain[format.HeaderBodyLenOffset:]); got != uint64(len(plain)) {
		t.Errorf("body_len = %d, want %d", got, len(plain))
	}
}

func TestOpenRejectsAbsurdBodyLen(t *testing.T) {
	encKey := intKey(13)
	salt := intSalt(14)

	// Authenticated header claiming a petabyte, with a tiny body attached.
	const absurd = 1 << 50
	blob, err := seal(encKey, salt, headerPlainFor(0, absurd), []byte("x"),
		fixedNonce(3), fixedNonce(4))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	header, body, err := Open(encKey, blob, 1<<20)
	if !errors.Is(err, ErrBodyLen) {
		t.Fatalf("Open = %v, want ErrBodyLen", err)
	}
	if header.BodyLen != absurd {
		t.Errorf("header body_len = %d, want %d", header.BodyLen, absurd)
	}
	if body != nil {
		t.Error("Open returned a body for an out-of-range length")
	}
}

func TestOpenRejectsLengthMismatch(t *testing.T) {
	encKey := intKey(15)
	salt := intSalt(16)

	// Header claims 5 body bytes, but only 3 are attached.
	blob, err := seal(encKey, salt, headerPlainFor(0, 5), []byte("abc"),
		fixedNonce(5), fixedNonce(6))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if _, _, err := Open(encKey, blob, 1<<20); !errors.Is(err, ErrDecrypt) {
		t.Errorf("Open with a short body = %v, want ErrDecrypt", err)
	}
}

func TestOpenBodyBindsHeader(t *testing.T) {
	encKey := intKey(17)
	salt := intSalt(18)
	plain := []byte("bound body")

	blob, err := seal(encKey, salt, headerPlainFor(0, uint64(len(plain))), plain,
		fixedNonce(7), fixedNonce(8))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	headerCT := append([]byte(nil), blob[format.HeaderCipherOffset:format.BodyNonceOffset]...)
	headerCT[0] ^= 0x01

	_, err = OpenBody(encKey, headerCT,
		blob[format.BodyNonceOffset:format.BodyCipherOffset], blob[format.BodyCipherOffset:])
	if !errors.Is(err, ErrDecrypt) {
		t.Errorf("OpenBody with a modified header_ct = %v, want ErrDecrypt", err)
	}
}

func TestOpenHeaderRejectsBadSizes(t *testing.T) {
	encKey := intKey(19)
	salt := intSalt(20)
	nonce := fixedNonce(9)

	if _, err := OpenHeader(encKey, salt[:8], nonce, make([]byte, format.HeaderCipherSize)); !errors.Is(err, ErrDecrypt) {
		t.Errorf("OpenHeader with a short salt = %v, want ErrDecrypt", err)
	}
	if _, err := OpenHeader(encKey, salt, nonce[:8], make([]byte, format.HeaderCipherSize)); !errors.Is(err, ErrDecrypt) {
		t.Errorf("OpenHeader with a short nonce = %v, want ErrDecrypt", err)
	}
	if _, err := OpenHeader(encKey, salt, nonce, make([]byte, format.HeaderCipherSize-1)); !errors.Is(err, ErrDecrypt) {
		t.Errorf("OpenHeader with a short header_ct = %v, want ErrDecrypt", err)
	}
}
