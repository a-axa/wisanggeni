package archive

import (
	"bytes"
	"errors"
	"testing"
)

// TestFitKeepsRawWhenCompressionDoesNotShrink covers the §5.1 "skip compress if
// not smaller" rule. A tar stream always carries its zero end-of-archive
// padding, which compresses away, so real payloads always shrink; passing empty
// bytes is the clean way to observe the non-shrinking branch: zstd still emits
// a frame, and that frame is larger than nothing.
func TestFitKeepsRawWhenCompressionDoesNotShrink(t *testing.T) {
	t.Parallel()

	got, err := fit(nil, 0)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if got.Compressed {
		t.Fatal("empty input must not be reported as compressed")
	}
	if len(got.Data) != 0 {
		t.Fatalf("Data = %d bytes, want 0", len(got.Data))
	}
}

// TestFitCompressesAndEnforcesLimit checks that a compressible payload is
// zstd-encoded and that compressible bytes still count against the budget.
func TestFitCompressesAndEnforcesLimit(t *testing.T) {
	t.Parallel()

	raw := bytes.Repeat([]byte("wisanggeni "), 4096)

	got, err := fit(raw, 0)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if !got.Compressed {
		t.Fatal("repetitive input was not compressed")
	}
	if int64(len(got.Data)) >= int64(len(raw)) {
		t.Fatalf("compressed %d >= raw %d", len(got.Data), len(raw))
	}

	// A budget below the compressed size must be rejected, proving the limit is
	// applied to the compressed output too.
	if _, err := fit(raw, int64(len(got.Data))-1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("fit over limit = %v, want ErrTooLarge", err)
	}
	if _, err := fit(raw, int64(len(got.Data))); err != nil {
		t.Fatalf("fit at limit = %v, want success", err)
	}
}

func TestLimitWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, max: 4}

	if n, err := lw.Write([]byte("1234")); err != nil || n != 4 {
		t.Fatalf("write within limit = (%d, %v), want (4, nil)", n, err)
	}
	if _, err := lw.Write([]byte("5")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write past limit = %v, want ErrTooLarge", err)
	}
	// A single oversized write is refused without touching the underlying writer.
	if _, err := lw.Write([]byte("123456789")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized write = %v, want ErrTooLarge", err)
	}
	if buf.String() != "1234" {
		t.Fatalf("underlying buffer = %q, want 1234", buf.String())
	}
}

func TestLimitWriterUnlimited(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, max: 0} // max <= 0 means no limit
	if _, err := lw.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatalf("unlimited write = %v, want nil", err)
	}
	if buf.Len() != 1024 {
		t.Fatalf("wrote %d bytes, want 1024", buf.Len())
	}
}
