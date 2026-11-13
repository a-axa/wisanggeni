package crypto

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/a-axa/wisanggeni/internal/format"
)

// ErrDecrypt is the single failure of Open: a wrong passphrase, a corrupted
// blob, a missing blob and a blob from another format all look the same. No
// plaintext marker, version or length is exposed before authentication (§11.1,
// §11.2), so the reader has nothing finer to report.
var ErrDecrypt = errors.New("crypto: decryption failed")

// ErrBodyLen reports an authenticated header whose body_len exceeds what the
// caller allows. It is checked before any allocation (§11.32); because the header
// is authenticated, reporting it separately leaks nothing.
var ErrBodyLen = errors.New("crypto: body length out of range")

// ErrSaltSize reports a salt that is not format.SaltSize bytes.
var ErrSaltSize = errors.New("crypto: bad salt size")

// errLayout reports internal sizes that do not match the frozen layout. Only
// seal can raise it, and only for a programming error.
var errLayout = errors.New("crypto: bad container layout sizes")

// Header is the decrypted header_plain (§6).
type Header struct {
	Version uint8
	Flags   uint8
	BodyLen uint64
}

// Seal encrypts plain into a container blob (§6):
//
//	salt | header_nonce | header_ct | body_nonce | body_ct
//
// salt is the 16-byte value already used to derive encKey, and it is also the
// header AEAD's AAD; header_ct becomes the body AEAD's AAD, binding the body to
// its header. Both nonces are generated here with crypto/rand (§11.4). flags are
// the header flags, for example format.FlagZstd set by the archive layer.
//
// The returned blob is exactly format.BlobOverhead + len(plain) bytes.
func Seal(encKey [32]byte, salt []byte, flags uint8, plain []byte) ([]byte, error) {
	if len(salt) != format.SaltSize {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSaltSize, len(salt), format.SaltSize)
	}

	var (
		headerNonce [format.NonceSize]byte
		bodyNonce   [format.NonceSize]byte
	)
	if _, err := cryptorand.Read(headerNonce[:]); err != nil {
		return nil, fmt.Errorf("crypto: header nonce: %w", err)
	}
	if _, err := cryptorand.Read(bodyNonce[:]); err != nil {
		return nil, fmt.Errorf("crypto: body nonce: %w", err)
	}

	headerPlain := make([]byte, format.HeaderPlainSize)
	headerPlain[format.HeaderVersionOffset] = format.Version
	headerPlain[format.HeaderFlagsOffset] = flags
	binary.LittleEndian.PutUint64(headerPlain[format.HeaderBodyLenOffset:], uint64(len(plain)))

	return seal(encKey, salt, headerPlain, plain, headerNonce[:], bodyNonce[:])
}

// seal is the layout engine behind Seal. It takes header_plain and the nonces
// explicitly so tests can pin the layout and construct hostile headers.
func seal(encKey [32]byte, salt, headerPlain, plain, headerNonce, bodyNonce []byte) ([]byte, error) {
	if len(salt) != format.SaltSize || len(headerPlain) != format.HeaderPlainSize ||
		len(headerNonce) != format.NonceSize || len(bodyNonce) != format.NonceSize {
		return nil, errLayout
	}

	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: XChaCha20-Poly1305: %w", err)
	}

	blob := make([]byte, format.BlobOverhead+len(plain))
	copy(blob[format.SaltOffset:], salt)
	copy(blob[format.HeaderNonceOffset:], headerNonce)

	// Header AEAD: AAD is the salt (§6).
	headerCT := aead.Seal(nil, headerNonce, headerPlain, salt)
	copy(blob[format.HeaderCipherOffset:], headerCT)

	copy(blob[format.BodyNonceOffset:], bodyNonce)

	// Body AEAD: AAD is header_ct, so the body cannot be moved to another
	// header or have its declared length rewritten.
	bodyCT := aead.Seal(nil, bodyNonce, plain, headerCT)
	copy(blob[format.BodyCipherOffset:], bodyCT)

	return blob, nil
}

// OpenHeader decrypts only the header (§7.9 step 6), for the reveal path and for
// inspect --header-only.
//
// salt is the 16 bytes read from the carrier's public positions S; salt,
// headerNonce and headerCT are the three pieces of the 80-byte prefix that the
// STC layer hands over. Any failure is ErrDecrypt.
func OpenHeader(encKey [32]byte, salt, headerNonce, headerCT []byte) (Header, error) {
	if len(salt) != format.SaltSize || len(headerNonce) != format.NonceSize ||
		len(headerCT) != format.HeaderCipherSize {
		return Header{}, ErrDecrypt
	}

	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		return Header{}, ErrDecrypt
	}

	plain, err := aead.Open(nil, headerNonce, headerCT, salt)
	if err != nil {
		return Header{}, ErrDecrypt
	}
	if len(plain) != format.HeaderPlainSize || plain[format.HeaderVersionOffset] != format.Version {
		return Header{}, ErrDecrypt
	}

	// Reserved bytes are deliberately not validated: a future v1 writer may use
	// them without breaking this reader, and the AEAD already guarantees they
	// were written by whoever holds the key.
	return Header{
		Version: plain[format.HeaderVersionOffset],
		Flags:   plain[format.HeaderFlagsOffset],
		BodyLen: binary.LittleEndian.Uint64(plain[format.HeaderBodyLenOffset:]),
	}, nil
}

// OpenBody decrypts the body (§7.9 step 8). headerCT is the body AEAD's AAD, so
// the caller must pass the exact header ciphertext the header was read from.
//
// The caller is responsible for validating h.BodyLen first (§11.32). Any failure
// is ErrDecrypt.
func OpenBody(encKey [32]byte, headerCT, bodyNonce, bodyCT []byte) ([]byte, error) {
	if len(headerCT) != format.HeaderCipherSize || len(bodyNonce) != format.NonceSize ||
		len(bodyCT) < format.TagSize {
		return nil, ErrDecrypt
	}

	aead, err := chacha20poly1305.NewX(encKey[:])
	if err != nil {
		return nil, ErrDecrypt
	}

	plain, err := aead.Open(nil, bodyNonce, bodyCT, headerCT)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}

// Open decrypts a whole blob at once, the writer's self-check and test path.
//
// maxBody is the largest body the caller can accept, normally Capacity(w,h). It
// is applied to the authenticated body_len before the body length is turned into
// a slice, so a hostile header cannot trigger a huge allocation (§11.32).
func Open(encKey [32]byte, blob []byte, maxBody uint64) (Header, []byte, error) {
	if len(blob) < format.BlobOverhead {
		return Header{}, nil, ErrDecrypt
	}

	header, err := OpenHeader(encKey,
		blob[format.SaltOffset:format.HeaderNonceOffset],
		blob[format.HeaderNonceOffset:format.HeaderCipherOffset],
		blob[format.HeaderCipherOffset:format.BodyNonceOffset],
	)
	if err != nil {
		return Header{}, nil, err
	}

	if header.BodyLen > maxBody {
		return header, nil, fmt.Errorf("%w: %d bytes over the %d-byte limit",
			ErrBodyLen, header.BodyLen, maxBody)
	}

	// A blob must be exactly its header plus its body; anything else means the
	// data was cut or padded, which is ErrDecrypt like any other corruption.
	if header.BodyLen > uint64(len(blob)) ||
		uint64(len(blob))-header.BodyLen != format.BlobOverhead {
		return header, nil, ErrDecrypt
	}

	body, err := OpenBody(encKey,
		blob[format.HeaderCipherOffset:format.BodyNonceOffset],
		blob[format.BodyNonceOffset:format.BodyCipherOffset],
		blob[format.BodyCipherOffset:],
	)
	if err != nil {
		return header, nil, err
	}
	return header, body, nil
}
