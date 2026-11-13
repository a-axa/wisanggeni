package crypto

import (
	"errors"
	"testing"

	"github.com/a-axa/wisanggeni/internal/format"
)

// FuzzOpen drives the container reader with arbitrary blobs. The reader is the
// only thing standing between an attacker-supplied PNG and the rest of the
// program, so its contract is: never panic, never allocate more than the caller
// allows, and report every failure as ErrDecrypt (or ErrBodyLen) so the error
// cannot be used as an oracle.
func FuzzOpen(f *testing.F) {
	key := [32]byte{0: 1, 1: 2, 2: 3}

	// A genuine blob, so the fuzzer starts from something the reader accepts.
	blob, err := Seal(key, make([]byte, format.SaltSize), 0, []byte("hello fuzz"))
	if err != nil {
		f.Fatalf("seal seed blob: %v", err)
	}
	f.Add(blob, uint64(0))
	f.Add(blob, uint64(1<<20))
	f.Add([]byte{}, uint64(1))
	f.Add(make([]byte, format.BlobOverhead), uint64(0))

	f.Fuzz(func(t *testing.T, data []byte, maxBody uint64) {
		if len(data) > 1<<16 {
			t.Skip()
		}
		// The limit itself must never be trusted to bound an allocation: the
		// reader checks the header length against both the limit and the blob
		// it was given, and either check alone would be enough to overflow.
		if maxBody > 1<<32 {
			maxBody = 1 << 32
		}
		header, body, err := Open(key, data, maxBody)
		if err != nil {
			switch {
			case errors.Is(err, ErrDecrypt):
			case errors.Is(err, ErrBodyLen):
			default:
				t.Fatalf("unexpected error %v for a %d-byte blob", err, len(data))
			}
			return
		}
		if header.Version != format.Version {
			t.Fatalf("accepted a blob with version %d", header.Version)
		}
		if uint64(len(data)) != header.BodyLen+format.BlobOverhead {
			t.Fatalf("accepted a blob of %d bytes claiming body length %d", len(data), header.BodyLen)
		}
		if uint64(len(body)) != header.BodyLen {
			t.Fatalf("returned %d body bytes, header says %d", len(body), header.BodyLen)
		}
	})
}

// FuzzOpenHeader covers the header alone, which reveal calls before it knows how
// long the body is. It must reject every short slice rather than panic.
func FuzzOpenHeader(f *testing.F) {
	key := [32]byte{0: 7}
	f.Add(make([]byte, format.SaltSize), make([]byte, format.NonceSize),
		make([]byte, format.HeaderCipherSize))

	f.Fuzz(func(t *testing.T, salt, nonce, ct []byte) {
		if len(salt) > 128 || len(nonce) > 128 || len(ct) > 256 {
			t.Skip()
		}
		if _, err := OpenHeader(key, salt, nonce, ct); err != nil && !errors.Is(err, ErrDecrypt) {
			t.Fatalf("unexpected error %v", err)
		}
	})
}

// FuzzDerive pins the key derivation against odd passphrases: any input either
// derives a key or fails cleanly, and the result is always deterministic.
//
// One derivation costs about a second of Argon2id by design, so the corpus stays
// tiny and the run is bounded by passes rather than by executions.
func FuzzDerive(f *testing.F) {
	salt := make([]byte, format.SaltSize)
	f.Add("correct horse battery staple", salt)
	f.Add("", salt)
	f.Add("Ｃａｆé", salt)

	f.Fuzz(func(t *testing.T, pass string, salt []byte) {
		if len(pass) > 64 || len(salt) > 64 {
			t.Skip()
		}
		keys, err := Derive(pass, salt)
		if err != nil {
			return // An empty salt is rejected; that is the documented contract.
		}
		again, err := Derive(pass, salt)
		if err != nil {
			t.Fatalf("second derive failed: %v", err)
		}
		if keys != again {
			t.Fatal("derive is not deterministic")
		}
	})
}
