package archive_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/a-axa/wisanggeni/internal/archive"
)

// tarEntry is one decoded member plus its regular-file content.
type tarEntry struct {
	hdr     *tar.Header
	content []byte
}

// readTar decompresses (when needed) and decodes a Packed payload. The tar
// reader hides PAX/GNU extension headers, so only real members come back.
func readTar(t *testing.T, p archive.Packed) []tarEntry {
	t.Helper()

	data := p.Data
	if p.Compressed {
		zr, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("zstd.NewReader: %v", err)
		}
		defer zr.Close()
		data, err = io.ReadAll(zr)
		if err != nil {
			t.Fatalf("zstd decode: %v", err)
		}
	}

	var out []tarEntry
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		var content []byte
		if hdr.Typeflag == tar.TypeReg {
			content, err = io.ReadAll(tr)
			if err != nil {
				t.Fatalf("tar read %q: %v", hdr.Name, err)
			}
		}
		out = append(out, tarEntry{hdr: hdr, content: content})
	}
	return out
}

// entryNames returns the archive names in stream order.
func entryNames(entries []tarEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.hdr.Name
	}
	return names
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rand.Uint32())
	}
	return b
}

func TestPackNoInput(t *testing.T) {
	t.Parallel()
	if _, err := archive.Pack(nil, archive.Options{}); !errors.Is(err, archive.ErrNoInput) {
		t.Fatalf("Pack(nil) = %v, want ErrNoInput", err)
	}
}

func TestPackNameCollision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "one", "same.txt")
	b := filepath.Join(dir, "two", "same.txt")
	write(t, a, []byte("a"))
	write(t, b, []byte("b"))

	_, err := archive.Pack([]string{a, b}, archive.Options{})
	if !errors.Is(err, archive.ErrNameCollision) {
		t.Fatalf("Pack = %v, want ErrNameCollision", err)
	}
	// The message must name both sides of the clash. Compare on the parent
	// directory names: the raw paths are %q-escaped, so comparing them
	// verbatim would fail on Windows where backslashes are doubled.
	if !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "two") {
		t.Fatalf("error %q does not name both inputs", err)
	}
}

func TestPackNameCollisionCaseInsensitive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := filepath.Join(dir, "A.txt")
	b := filepath.Join(dir, "a.txt")
	write(t, a, []byte("A"))
	write(t, b, []byte("a"))

	if _, err := archive.Pack([]string{a, b}, archive.Options{}); !errors.Is(err, archive.ErrNameCollision) {
		t.Fatalf("Pack = %v, want ErrNameCollision", err)
	}
}

func TestPackDuplicateInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "dup.txt")
	write(t, f, []byte("x"))

	if _, err := archive.Pack([]string{f, f}, archive.Options{}); !errors.Is(err, archive.ErrNameCollision) {
		t.Fatalf("Pack = %v, want ErrNameCollision", err)
	}
}

func TestPackBadBasename(t *testing.T) {
	t.Parallel()
	// "" and "/" have no usable entry name.
	if _, err := archive.Pack([]string{"/"}, archive.Options{}); !errors.Is(err, archive.ErrBadInput) {
		t.Fatalf("Pack(/) = %v, want ErrBadInput", err)
	}
}

func TestPackMissingInput(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	if _, err := archive.Pack([]string{missing}, archive.Options{}); !errors.Is(err, archive.ErrBadInput) {
		t.Fatalf("Pack(missing) = %v, want ErrBadInput", err)
	}
}

func TestPackSymlinkInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	write(t, target, []byte("data"))
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := archive.Pack([]string{link}, archive.Options{}); !errors.Is(err, archive.ErrUnsupported) {
		t.Fatalf("Pack(symlink) = %v, want ErrUnsupported", err)
	}
}

func TestPackSymlinkInsideFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	write(t, filepath.Join(root, "a.txt"), []byte("a"))
	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if _, err := archive.Pack([]string{root}, archive.Options{}); !errors.Is(err, archive.ErrUnsupported) {
		t.Fatalf("Pack(folder with symlink) = %v, want ErrUnsupported", err)
	}
}

func TestPackEmptyFolder(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "emptyfolder")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	p, err := archive.Pack([]string{root}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	entries := readTar(t, p)
	names := entryNames(entries)

	for _, want := range []string{"emptyfolder/", "emptyfolder/sub/"} {
		if !contains(names, want) {
			t.Fatalf("names %v missing directory entry %q", names, want)
		}
	}
	for _, e := range entries {
		if e.hdr.Typeflag != tar.TypeDir {
			t.Fatalf("entry %q typeflag = %d, want TypeDir", e.hdr.Name, e.hdr.Typeflag)
		}
	}
}

func TestPackAbsoluteInputUsesBasename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "note.txt")
	write(t, f, []byte("hello"))

	abs, err := filepath.Abs(f)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	p, err := archive.Pack([]string{abs}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	entries := readTar(t, p)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].hdr.Name != "note.txt" {
		t.Fatalf("name = %q, want note.txt", entries[0].hdr.Name)
	}
	// The absolute path, and its parent directories, must not leak into the
	// archive anywhere.
	raw, err := io.ReadAll(zstdOrRaw(t, p))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if bytes.Contains(raw, []byte(dir)) {
		t.Fatalf("archive leaks absolute path %q", dir)
	}
}

func TestPackCarrierInside(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	carrier := filepath.Join(root, "out.png")
	write(t, filepath.Join(root, "a.txt"), []byte("a"))
	write(t, carrier, []byte("not really a png"))

	_, err := archive.Pack([]string{root}, archive.Options{Exclude: []string{carrier}})
	if !errors.Is(err, archive.ErrCarrierInside) {
		t.Fatalf("Pack = %v, want ErrCarrierInside", err)
	}
}

func TestPackCarrierInsideViaHardlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	inside := filepath.Join(root, "a.txt")
	write(t, inside, []byte("a"))
	link := filepath.Join(dir, "alias.txt")
	if err := os.Link(inside, link); err != nil {
		t.Skipf("hardlink unsupported: %v", err)
	}

	// The excluded path is a different name but the same file, so SameFile
	// must catch it.
	_, err := archive.Pack([]string{root}, archive.Options{Exclude: []string{link}})
	if !errors.Is(err, archive.ErrCarrierInside) {
		t.Fatalf("Pack = %v, want ErrCarrierInside", err)
	}
}

func TestPackExcludeOutsideIsFine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	write(t, filepath.Join(root, "a.txt"), []byte("a"))
	other := filepath.Join(dir, "elsewhere.png")
	write(t, other, []byte("pixels"))

	if _, err := archive.Pack([]string{root}, archive.Options{Exclude: []string{other}}); err != nil {
		t.Fatalf("Pack = %v, want success", err)
	}
	// A not-yet-created output path must also be accepted.
	missing := filepath.Join(dir, "new.png")
	if _, err := archive.Pack([]string{root}, archive.Options{Exclude: []string{missing}}); err != nil {
		t.Fatalf("Pack with missing exclude = %v, want success", err)
	}
}

func TestPackRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	big := randBytes(1 << 20) // 1 MiB
	write(t, filepath.Join(dir, "src", "empty.bin"), nil)
	write(t, filepath.Join(dir, "src", "big.bin"), big)
	write(t, filepath.Join(dir, "src", "a", "b", "deep.txt"), []byte("deep"))
	write(t, filepath.Join(dir, "src", "café-nfc.txt"), []byte("nfc"))
	write(t, filepath.Join(dir, "src", "cafe\u0301-nfd.txt"), []byte("nfd"))

	p, err := archive.Pack([]string{filepath.Join(dir, "src")}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	got := make(map[string][]byte)
	for _, e := range readTar(t, p) {
		if e.hdr.Typeflag == tar.TypeReg {
			got[e.hdr.Name] = e.content
		}
	}

	want := map[string][]byte{
		"src/empty.bin":          {},
		"src/big.bin":            big,
		"src/a/b/deep.txt":       []byte("deep"),
		"src/café-nfc.txt":       []byte("nfc"),
		"src/cafe\u0301-nfd.txt": []byte("nfd"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files (%v), want %d", len(got), keys(got), len(want))
	}
	for name, content := range want {
		gc, ok := got[name]
		if !ok {
			t.Fatalf("missing entry %q in %v", name, keys(got))
		}
		if !bytes.Equal(gc, content) {
			t.Fatalf("entry %q content mismatch (%d vs %d bytes)", name, len(gc), len(content))
		}
	}
}

func TestPackDeterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "p", "a.txt"), []byte("alpha"))
	write(t, filepath.Join(dir, "p", "b.txt"), randBytes(4096))
	write(t, filepath.Join(dir, "p", "sub", "c.txt"), []byte("gamma"))

	in := []string{filepath.Join(dir, "p")}
	first, err := archive.Pack(in, archive.Options{})
	if err != nil {
		t.Fatalf("Pack 1: %v", err)
	}
	second, err := archive.Pack(in, archive.Options{})
	if err != nil {
		t.Fatalf("Pack 2: %v", err)
	}
	if first.Compressed != second.Compressed || !bytes.Equal(first.Data, second.Data) {
		t.Fatalf("Pack is not deterministic (%d vs %d bytes)", len(first.Data), len(second.Data))
	}
}

func TestPackMetadata(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "meta")
	file := filepath.Join(root, "secret.txt")
	write(t, file, []byte("payload"))
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Windows has no POSIX permission bits: chmod only toggles the read-only
	// flag, so the round-tripped mode is 0666 regardless.
	wantMode := int64(0o640)
	if runtime.GOOS == "windows" {
		wantMode = 0o666
	}

	p, err := archive.Pack([]string{root}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	var found bool
	for _, e := range readTar(t, p) {
		if e.hdr.Uid != 0 || e.hdr.Gid != 0 {
			t.Fatalf("%q uid/gid = %d/%d, want 0/0", e.hdr.Name, e.hdr.Uid, e.hdr.Gid)
		}
		if e.hdr.Uname != "" || e.hdr.Gname != "" {
			t.Fatalf("%q uname/gname = %q/%q, want empty", e.hdr.Name, e.hdr.Uname, e.hdr.Gname)
		}
		if !isZeroTime(e.hdr.ModTime) {
			t.Fatalf("%q mtime = %v, want zero", e.hdr.Name, e.hdr.ModTime)
		}
		if !isZeroTime(e.hdr.AccessTime) || !isZeroTime(e.hdr.ChangeTime) {
			t.Fatalf("%q atime/ctime = %v/%v, want zero", e.hdr.Name, e.hdr.AccessTime, e.hdr.ChangeTime)
		}
		if e.hdr.Name != "meta/secret.txt" {
			continue
		}
		found = true
		if e.hdr.Mode != wantMode {
			t.Fatalf("mode = %o, want %o", e.hdr.Mode, wantMode)
		}
	}
	if !found {
		t.Fatal("secret.txt header not found")
	}
}

func TestPackEntryOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root := filepath.Join(dir, "ord")
	write(t, filepath.Join(root, "b.txt"), []byte("b"))
	write(t, filepath.Join(root, "a.txt"), []byte("a"))
	write(t, filepath.Join(root, "Z.txt"), []byte("Z"))
	write(t, filepath.Join(root, "sub", "x.txt"), []byte("x"))

	p, err := archive.Pack([]string{root}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	names := entryNames(readTar(t, p))

	// Sorted by raw name bytes: uppercase 'Z' (0x5A) sorts before lowercase.
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for i := range names {
		if names[i] != sorted[i] {
			t.Fatalf("entry order %v is not sorted; want %v", names, sorted)
		}
	}
}

func TestPackCompressesRepetitive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rep.txt"), bytes.Repeat([]byte("wisanggeni "), 4096))

	p, err := archive.Pack([]string{filepath.Join(dir, "rep.txt")}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !p.Compressed {
		t.Fatal("repetitive payload was not compressed")
	}
	// Content must survive the round-trip.
	entries := readTar(t, p)
	if len(entries) != 1 || len(entries[0].content) != len(bytes.Repeat([]byte("wisanggeni "), 4096)) {
		t.Fatalf("round-trip after compression failed: %d entries", len(entries))
	}
}

func TestPackMaxBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "data.bin")
	write(t, f, randBytes(64<<10))

	// No limit: succeeds, and its size is the floor.
	full, err := archive.Pack([]string{f}, archive.Options{})
	if err != nil {
		t.Fatalf("Pack unlimited: %v", err)
	}
	floor := int64(len(full.Data))

	// Exactly the floor: succeeds.
	if _, err := archive.Pack([]string{f}, archive.Options{MaxBytes: floor}); err != nil {
		t.Fatalf("Pack at limit = %v, want success", err)
	}

	// One byte under the smallest possible output: rejected with exit-2 error.
	if _, err := archive.Pack([]string{f}, archive.Options{MaxBytes: floor - 1}); !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("Pack over limit = %v, want ErrTooLarge", err)
	}
}

// --- helpers ---------------------------------------------------------------

// zstdOrRaw returns a reader over the decoded tar bytes.
func zstdOrRaw(t *testing.T, p archive.Packed) io.Reader {
	t.Helper()
	if !p.Compressed {
		return bytes.NewReader(p.Data)
	}
	zr, err := zstd.NewReader(bytes.NewReader(p.Data))
	if err != nil {
		t.Fatalf("zstd.NewReader: %v", err)
	}
	return zr
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// isZeroTime reports whether a timestamp carries no source information. The
// tar writer stores the zero time as Unix epoch 0, and the reader decodes that
// back to unix(0,0), so both representations mean "zeroed".
func isZeroTime(t time.Time) bool {
	return t.IsZero() || (t.Unix() == 0 && t.Nanosecond() == 0)
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
