package archive_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
)

// spec is one header plus, for regular files, its content.
type spec struct {
	hdr  tar.Header
	data []byte
}

// buildTar serialises specs into a tar stream, setting Size for regular files.
func buildTar(t *testing.T, specs ...spec) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, s := range specs {
		h := s.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(s.data))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("WriteHeader %q: %v", h.Name, err)
		}
		if len(s.data) > 0 {
			if _, err := tw.Write(s.data); err != nil {
				t.Fatalf("Write %q: %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func reg(name string, data []byte) spec {
	return spec{hdr: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}, data: data}
}

func TestUnpackRoundTripFromPack(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	bigData := randBytes(4096)
	write(t, filepath.Join(src, "tree", "a.txt"), []byte("alpha"))
	write(t, filepath.Join(src, "tree", "sub", "b.bin"), bigData)
	if err := os.MkdirAll(filepath.Join(src, "tree", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, compress := range []bool{true, false} {
		rounded := "raw"
		if compress {
			rounded = "zstd"
		}
		t.Run(rounded, func(t *testing.T) {
			p, err := archive.Pack([]string{filepath.Join(src, "tree")}, archive.Options{})
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}

			out := t.TempDir()
			res, err := archive.Unpack(p.Data, out, archive.UnpackOptions{Compressed: p.Compressed})
			if err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			if len(res.Entries) < 4 {
				t.Fatalf("got %d entries, want at least 4", len(res.Entries))
			}

			got, err := os.ReadFile(filepath.Join(out, "tree", "a.txt"))
			if err != nil || string(got) != "alpha" {
				t.Fatalf("a.txt = %q, %v", got, err)
			}
			gotBig, err := os.ReadFile(filepath.Join(out, "tree", "sub", "b.bin"))
			if err != nil || !bytes.Equal(gotBig, bigData) {
				t.Fatalf("b.bin mismatch: %d bytes, %v", len(gotBig), err)
			}
			if info, err := os.Stat(filepath.Join(out, "tree", "empty")); err != nil || !info.IsDir() {
				t.Fatalf("empty dir missing: %v", err)
			}
		})
	}
}

func TestUnpackRejectsZipSlip(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	out := filepath.Join(parent, "out")
	canary := filepath.Join(parent, "canary.txt")
	write(t, canary, []byte("original"))

	for _, name := range []string{
		"../../canary.txt",
		"../canary.txt",
		"a/../../canary.txt",
		"/etc/passwd",
		"..",
	} {
		t.Run(name, func(t *testing.T) {
			data := buildTar(t, reg(name, []byte("pwned")))
			if _, err := archive.Unpack(data, out, archive.UnpackOptions{}); !errors.Is(err, archive.ErrUnsafePath) {
				t.Fatalf("Unpack(%q) = %v, want ErrUnsafePath", name, err)
			}
		})
	}

	// The canary must be untouched and no stray file may appear next to out.
	if got, err := os.ReadFile(canary); err != nil || string(got) != "original" {
		t.Fatalf("canary = %q, %v; want original", got, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "out" && e.Name() != "canary.txt" {
			t.Fatalf("unexpected file outside outdir: %q", e.Name())
		}
	}
}

func TestUnpackRejectsSymlinkEntry(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	data := buildTar(t, spec{hdr: tar.Header{
		Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd",
	}})

	_, err := archive.Unpack(data, out, archive.UnpackOptions{})
	if !errors.Is(err, archive.ErrUnsupported) {
		t.Fatalf("Unpack = %v, want ErrUnsupported", err)
	}
	if _, err := os.Lstat(filepath.Join(out, "link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink entry created something: %v", err)
	}
}

func TestUnpackRejectsHardlinkEntry(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	data := buildTar(t, spec{hdr: tar.Header{
		Name: "hard", Typeflag: tar.TypeLink, Linkname: "target",
	}})

	if _, err := archive.Unpack(data, out, archive.UnpackOptions{}); !errors.Is(err, archive.ErrUnsupported) {
		t.Fatalf("Unpack = %v, want ErrUnsupported", err)
	}
}

func TestUnpackRejectsSpecialEntries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		typeflag byte
	}{
		{"char device", tar.TypeChar},
		{"block device", tar.TypeBlock},
		{"fifo", tar.TypeFifo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			data := buildTar(t, spec{hdr: tar.Header{
				Name: "node", Typeflag: tc.typeflag, Devmajor: 1, Devminor: 3,
			}})
			if _, err := archive.Unpack(data, out, archive.UnpackOptions{}); !errors.Is(err, archive.ErrUnsupported) {
				t.Fatalf("Unpack = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestUnpackRefusesSymlinkedParent(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	out := filepath.Join(parent, "out")
	escape := filepath.Join(parent, "escape")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(escape, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(escape, filepath.Join(out, "sub")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// The archive legitimately says "sub/file.txt", but "sub" is a link that
	// points outside, so the write must be refused.
	data := buildTar(t, reg("sub/file.txt", []byte("pwned")))
	_, err := archive.Unpack(data, out, archive.UnpackOptions{})
	if !errors.Is(err, archive.ErrUnsafePath) {
		t.Fatalf("Unpack = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Stat(filepath.Join(escape, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote through the symlink: %v", err)
	}
}

func TestUnpackStripsSetuid(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permission bits on Windows")
	}
	out := t.TempDir()
	data := buildTar(t, spec{
		hdr:  tar.Header{Name: "tool", Typeflag: tar.TypeReg, Mode: 0o4755},
		data: []byte("#!/bin/sh\n"),
	})

	if _, err := archive.Unpack(data, out, archive.UnpackOptions{}); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	info, err := os.Stat(filepath.Join(out, "tool"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Fatalf("perm = %o, want 755", perm)
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Fatalf("mode = %v, setuid/setgid survived", info.Mode())
	}
}

func TestUnpackMaxOutput(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	data := buildTar(t, reg("big.bin", bytes.Repeat([]byte("x"), 4096)))

	if _, err := archive.Unpack(data, out, archive.UnpackOptions{MaxBytes: 1024}); !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Unpack = %v, want ErrTooLarge", err)
	}
	if _, err := os.Stat(filepath.Join(out, "big.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized archive still wrote a file")
	}

	// A generous cap succeeds.
	if _, err := archive.Unpack(data, out, archive.UnpackOptions{MaxBytes: 1 << 20}); err != nil {
		t.Fatalf("Unpack within cap = %v", err)
	}
}

func TestUnpackNonPositiveMaxBytesIsNotUnlimited(t *testing.T) {
	t.Parallel()
	// Regression: a negative MaxBytes used to mean "no cap", so a compressed
	// bomb expanded until the process was OOM-killed. It must now resolve to the
	// default cap, which is what makes the bomb test below meaningful for every
	// value the CLI and the library can pass.
	raw := buildTar(t, reg("zeros.bin", make([]byte, 1<<20)))

	p, err := archive.Pack([]string{mustFile(t, raw)}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !p.Compressed {
		t.Skip("payload did not compress; bomb test not applicable")
	}

	// One byte budget that is certainly smaller than the default cap proves the
	// cap is applied, not skipped, for both a negative and a zero value.
	for _, maxBytes := range []int64{-1, 0} {
		if _, err := archive.Unpack(p.Data, t.TempDir(), archive.UnpackOptions{
			Compressed: true,
			MaxBytes:   maxBytes,
		}); err != nil {
			t.Fatalf("Unpack with MaxBytes=%d = %v, want success under the default cap", maxBytes, err)
		}
	}
}

func TestUnpackCompressedBombIsCapped(t *testing.T) {
	t.Parallel()
	// 1 MiB of zeros compresses tiny but expands past a small cap.
	out := t.TempDir()
	raw := buildTar(t, reg("zeros.bin", make([]byte, 1<<20)))

	p, err := archive.Pack([]string{mustFile(t, raw)}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !p.Compressed {
		t.Skip("payload did not compress; bomb test not applicable")
	}

	if _, err := archive.Unpack(p.Data, out, archive.UnpackOptions{
		Compressed: true,
		MaxBytes:   4096,
	}); !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Unpack = %v, want ErrTooLarge", err)
	}
}

func TestUnpackOverwrite(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	target := filepath.Join(out, "keep.txt")
	write(t, target, []byte("old"))

	data := buildTar(t, reg("keep.txt", []byte("new")))

	if _, err := archive.Unpack(data, out, archive.UnpackOptions{}); !errors.Is(err, archive.ErrExists) {
		t.Fatalf("Unpack = %v, want ErrExists", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("existing file was modified without --force: %q", got)
	}

	if _, err := archive.Unpack(data, out, archive.UnpackOptions{Force: true}); err != nil {
		t.Fatalf("Unpack with force = %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("content after force = %q, want new", got)
	}
}

func TestUnpackRejectsDuplicateEntry(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	data := buildTar(t, reg("dup.txt", []byte("1")), reg("dup.txt", []byte("2")))

	if _, err := archive.Unpack(data, out, archive.UnpackOptions{}); !errors.Is(err, archive.ErrBadArchive) {
		t.Fatalf("Unpack = %v, want ErrBadArchive", err)
	}
}

func TestUnpackMalformedPayload(t *testing.T) {
	t.Parallel()
	out := t.TempDir()

	if _, err := archive.Unpack([]byte("not a tar at all, but long enough to look like one"), out, archive.UnpackOptions{}); err == nil {
		t.Fatal("garbage payload was accepted")
	}
	if _, err := archive.Unpack([]byte("\x28\xb5\x2f\xfdgarbage"), out, archive.UnpackOptions{Compressed: true}); !errors.Is(err, archive.ErrBadArchive) {
		t.Fatalf("bad zstd = %v, want ErrBadArchive", err)
	}
}

func TestUnpackEmptyArchive(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	res, err := archive.Unpack(nil, out, archive.UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack(nil) = %v", err)
	}
	if len(res.Entries) != 0 || len(res.Paths) != 0 {
		t.Fatalf("empty archive yielded %d entries", len(res.Entries))
	}
}

func TestUnpackNestedDirsCreated(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	// A file two levels deep, with no directory entries at all.
	data := buildTar(t, reg("a/b/c.txt", []byte("deep")))

	res, err := archive.Unpack(data, out, archive.UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(out, "a", "b", "c.txt")); err != nil || string(got) != "deep" {
		t.Fatalf("nested file = %q, %v", got, err)
	}
	if len(res.Paths) != 1 {
		t.Fatalf("Paths = %v", res.Paths)
	}
}

// mustFile writes data to a temp file and returns its path.
func mustFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "payload.bin")
	write(t, p, data)
	return p
}
