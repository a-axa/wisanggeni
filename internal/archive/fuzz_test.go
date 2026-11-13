package archive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// fuzzTB is the slice of *testing.T and *testing.F the seed builders need.
// Taking an interface lets the same builders serve both the seed phase and the
// plain-test assertions without triplicating them.
type fuzzTB interface {
	Helper()
	Fatalf(format string, args ...any)
	TempDir() string
}

// The payload reaches Unpack after passing through the container, so it is
// attacker-controlled in the threat model that matters: a hidden file the user
// was tricked into opening. The two contracts that carry the most weight are
// "never panic" and "never write outside the requested directory", so both are
// fuzzed directly rather than only through the hand-written cases.
//
// Run these with a bounded minimization budget:
//
//	go test ./internal/archive -run XXX -fuzz FuzzList -fuzzminimizetime 3s
//
// Without -fuzzminimizetime the minimizer can rewrite a compressed seed into one
// that expands to the 1 GiB default cap, and a single such input eats the whole
// run's budget while looking like a hang. The archive package bounds that
// expansion, so the run does finish; capping minimization just keeps throughput
// up. Decoding is always bounded by the cap, never by the input length.

// FuzzList drives the read-only path, which `inspect` reaches without a
// passphrase. It must survive arbitrary bytes: neither the tar reader nor the
// zstd decoder may be trusted to reject garbage before the other checks run.
func FuzzList(f *testing.F) {
	seedTar := smallTar(f)

	f.Add(seedTar, false, int64(0))
	f.Add(seedTar, false, int64(1<<20))
	f.Add(seedTar, false, int64(-1))
	f.Add([]byte{}, false, int64(0))
	f.Add([]byte("not a tar at all"), false, int64(0))
	f.Add(zstdOf(f, seedTar), true, int64(1<<20))
	f.Add(zstdOf(f, seedTar), false, int64(1<<20))

	f.Fuzz(func(t *testing.T, data []byte, compressed bool, maxBytes int64) {
		if len(data) > 1<<18 {
			t.Skip()
		}
		maxBytes = clampMaxBytes(maxBytes)

		entries, err := List(data, ListOptions{Compressed: compressed, MaxBytes: maxBytes})
		if err != nil {
			return
		}
		for _, e := range entries {
			rel, err := safeRel(e.Name)
			if err != nil {
				t.Fatalf("listed an unsafe name %q: %v", e.Name, err)
			}
			if rel != e.Name {
				t.Fatalf("listed an uncleaned name %q, safe form is %q", e.Name, rel)
			}
			if e.Size < 0 {
				t.Fatalf("listed entry %q with negative size %d", e.Name, e.Size)
			}
		}
	})
}

// FuzzUnpack drives the writing path. Beyond "never panic" it asserts the
// security property the whole package exists for: whatever the archive claims,
// nothing is created outside dir. Every iteration gets a fresh temporary
// directory and a snapshot of its parent, so a single escaping write fails the
// target instead of silently polluting the tree.
func FuzzUnpack(f *testing.F) {
	seedTar := smallTar(f)

	f.Add(seedTar, false, int64(0), false)
	f.Add(seedTar, false, int64(1<<20), true)
	f.Add(zstdOf(f, seedTar), true, int64(1<<20), false)
	f.Add(maliciousTar(f, "../../escape"), false, int64(0), false)
	f.Add(maliciousTar(f, "/tmp/absolute"), false, int64(0), true)
	f.Add(maliciousTar(f, "link"), false, int64(0), false)
	f.Add(maliciousTar(f, "deep/../../../escape2"), false, int64(0), false)
	f.Add([]byte{}, false, int64(0), false)

	f.Fuzz(func(t *testing.T, data []byte, compressed bool, maxBytes int64, force bool) {
		if len(data) > 1<<18 {
			t.Skip()
		}
		maxBytes = clampMaxBytes(maxBytes)

		// dir lives inside a parent we can inspect afterwards, so an escape lands
		// somewhere we can still see it.
		parent := t.TempDir()
		dir := filepath.Join(parent, "out")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		opts := UnpackOptions{Compressed: compressed, MaxBytes: maxBytes, Force: force}
		result, err := Unpack(data, dir, opts)
		if err != nil {
			// A refused archive must leave no trace outside dir. It may leave a
			// half-written tree inside dir, which is allowed (and documented).
			assertContained(t, parent, dir)
			return
		}

		assertContained(t, parent, dir)
		for _, p := range result.Paths {
			if !within(dir, p) {
				t.Fatalf("unpack reported a path outside dir: %q", p)
			}
		}
		if len(result.Paths) != len(result.Entries) {
			t.Fatalf("got %d paths for %d entries", len(result.Paths), len(result.Entries))
		}
	})
}

// clampMaxBytes keeps the fuzzer's cap in a range the product can actually
// produce. The CLI rejects a non-positive --max-output, so the realistic
// domain is positive; negative and zero are still passed through because the
// archive package must treat them as its default cap rather than as unlimited,
// which is exactly the property the bomb test pins. The upper bound keeps a
// single io.ReadAll comfortably below the worker's memory limit.
func clampMaxBytes(maxBytes int64) int64 {
	const hi = 64 << 20
	if maxBytes > hi {
		return hi
	}
	if maxBytes < -hi {
		return -hi
	}
	return maxBytes
}

// assertContained fails if any entry in parent, other than out/ itself, appears
// at the top level: that is what an escaping write would look like.
func assertContained(t *testing.T, parent, dir string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read %q: %v", parent, err)
	}
	for _, e := range entries {
		if filepath.Join(parent, e.Name()) == dir {
			continue
		}
		t.Fatalf("unpack created %q outside the output directory", e.Name())
	}
}

// within reports whether p is dir or below it.
func within(dir, p string) bool {
	dir = filepath.Clean(dir)
	p = filepath.Clean(p)
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(filepath.Separator))
}

// smallTar is a genuine, valid archive used as the positive seed: one directory
// and one file, packed the way the tool packs them.
func smallTar(t fuzzTB) []byte {
	t.Helper()
	src := t.TempDir()
	if err := os.Mkdir(filepath.Join(src, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "docs", "a.txt"), []byte("hello fuzz"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	packed, err := Pack([]string{src}, Options{NoCompress: true})
	if err != nil {
		t.Fatalf("pack seed: %v", err)
	}
	return packed.Data
}

// zstdOf compresses data so the Compressed path gets a real seed rather than
// only random bytes that fail at the first frame header check.
func zstdOf(t fuzzTB, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

// maliciousTar builds a tar with one entry whose raw header name is exactly the
// attacker's string, kept unmangled so "..", absolute and link seeds reach the
// validator as an attacker would write them.
func maliciousTar(t fuzzTB, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len("payload")),
		Typeflag: tar.TypeReg,
	}
	if name == "link" {
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = "target"
		hdr.Size = 0
	}
	if err := tw.WriteHeader(hdr); err != nil {
		// archive/tar rejects some names outright, which is itself a fine
		// outcome: fall back to an empty slice so the seed still exists.
		return nil
	}
	if hdr.Typeflag == tar.TypeReg {
		if _, err := tw.Write([]byte("payload")); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

// TestFuzzSeedsAreUsable keeps the seed builders on the public surface, so a
// signature drift is caught by a plain `go test` even when the fuzzers are not
// run.
func TestFuzzSeedsAreUsable(t *testing.T) {
	seed := smallTar(t)
	entries, err := List(seed, ListOptions{})
	if err != nil {
		t.Fatalf("list seed: %v", err)
	}
	if len(entries) != 3 { // the root directory, docs/ and docs/a.txt
		t.Fatalf("seed has %d entries, want 3", len(entries))
	}

	dir := t.TempDir()
	result, err := Unpack(seed, dir, UnpackOptions{})
	if err != nil {
		t.Fatalf("unpack seed: %v", err)
	}
	if len(result.Paths) != 3 {
		t.Fatalf("unpacked %d paths, want 3", len(result.Paths))
	}
	for _, p := range result.Paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("stat %q: %v", p, err)
		}
	}

	for _, evil := range [][]byte{
		maliciousTar(t, "../../escape"),
		maliciousTar(t, "/tmp/absolute"),
	} {
		if _, err := Unpack(evil, t.TempDir(), UnpackOptions{}); err == nil {
			t.Fatal("unpack accepted an escaping archive")
		}
	}
}
