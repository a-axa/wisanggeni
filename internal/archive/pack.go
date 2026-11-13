// Package archive serialises the hidden payload. Pack builds a deterministic
// tar stream with cleaned metadata and, when it helps, compresses it with zstd
// (§5.1, §7.8 step 4). Unpack reverses that safely.
package archive

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

var (
	// ErrNoInput reports a Pack call with nothing to pack.
	ErrNoInput = errors.New("archive: no input")

	// ErrBadInput reports an input path that is missing, unreadable or unusable.
	ErrBadInput = errors.New("archive: bad input")

	// ErrNameCollision reports two inputs whose root names collide, including
	// when they differ only by letter case (§5.1).
	ErrNameCollision = errors.New("archive: duplicate root name")

	// ErrUnsupported reports a symlink, device, FIFO or socket (§5.1).
	ErrUnsupported = errors.New("archive: unsupported file type")

	// ErrCarrierInside reports the carrier or output appearing inside an input
	// folder, which would make the image wrap itself (§5.1).
	ErrCarrierInside = errors.New("archive: carrier or output is inside an input folder")

	// ErrTooLarge reports a payload that cannot fit the caller's byte budget.
	// The CLI maps it to exit code 2.
	ErrTooLarge = errors.New("archive: payload exceeds the available capacity")
)

// Options controls Pack.
type Options struct {
	// MaxBytes is the largest payload Pack may return, normally Capacity(w,h).
	// Zero or negative means no limit.
	MaxBytes int64

	// Exclude lists the carrier and output paths. If one of them is found
	// inside an input folder the walk fails with ErrCarrierInside.
	Exclude []string
}

// Packed is the payload for the container body.
type Packed struct {
	// Data is the tar stream, optionally zstd-compressed.
	Data []byte

	// Compressed reports whether Data is zstd and the header must set
	// format.FlagZstd.
	Compressed bool
}

// entry is one tar member before serialisation.
type entry struct {
	name string // archive path, slash separated, never absolute
	path string // filesystem path to read
	info fs.FileInfo
	dir  bool
}

// root is one resolved input.
type root struct {
	path string // absolute filesystem path
	name string // archive root name (basename)
}

// Pack serialises inputs into a deterministic tar, compresses it when that
// makes it smaller, and enforces maxBytes (§5.1).
//
// Entry names are the basename of each input plus, for folders, the path
// relative to it. Inputs are sorted by name bytes and metadata is stripped
// (uid/gid 0, empty owners, zero times, permission bits only), so packing the
// same inputs twice yields byte-identical output.
func Pack(inputs []string, opts Options) (Packed, error) {
	if len(inputs) == 0 {
		return Packed{}, ErrNoInput
	}

	roots, err := collectRoots(inputs)
	if err != nil {
		return Packed{}, err
	}

	excluded, err := statExcluded(opts.Exclude)
	if err != nil {
		return Packed{}, err
	}

	entries := make([]entry, 0, len(roots))
	for _, r := range roots {
		if err := walkRoot(r, excluded, &entries); err != nil {
			return Packed{}, err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	tarData, err := writeTar(entries)
	if err != nil {
		return Packed{}, err
	}

	return fit(tarData, opts.MaxBytes)
}

// collectRoots resolves each input and rejects the cases §5.1 calls errors:
// no usable basename, unsupported file type, and colliding root names.
func collectRoots(inputs []string) ([]root, error) {
	seen := make(map[string]string, len(inputs))
	roots := make([]root, 0, len(inputs))

	for _, in := range inputs {
		abs, err := filepath.Abs(in)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrBadInput, in, err)
		}

		name := filepath.Base(abs)
		if name == "/" || name == `\` || name == "." || name == ".." || name == "" {
			return nil, fmt.Errorf("%w: %q has no usable name", ErrBadInput, in)
		}

		// Case-insensitive comparison: the archive may be extracted on a
		// case-insensitive filesystem, where "A" and "a" would collide.
		key := strings.ToLower(name)
		if prev, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: %q and %q", ErrNameCollision, prev, in)
		}
		seen[key] = in

		info, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrBadInput, in, err)
		}
		if err := checkType(info); err != nil {
			return nil, fmt.Errorf("%w: %q", err, in)
		}

		roots = append(roots, root{path: abs, name: name})
	}
	return roots, nil
}

// checkType allows only regular files and directories (§5.1).
func checkType(info fs.FileInfo) error {
	mode := info.Mode()
	if mode.IsRegular() || mode.IsDir() {
		return nil
	}
	switch {
	case mode&fs.ModeSymlink != 0:
		return fmt.Errorf("%w: symlink", ErrUnsupported)
	case mode&fs.ModeDevice != 0:
		return fmt.Errorf("%w: device", ErrUnsupported)
	case mode&fs.ModeNamedPipe != 0:
		return fmt.Errorf("%w: FIFO", ErrUnsupported)
	case mode&fs.ModeSocket != 0:
		return fmt.Errorf("%w: socket", ErrUnsupported)
	default:
		return fmt.Errorf("%w: mode %s", ErrUnsupported, mode)
	}
}

// statExcluded resolves the carrier and output paths once. Paths that do not
// exist yet cannot be inside a walked tree, so they are skipped.
func statExcluded(paths []string) ([]fs.FileInfo, error) {
	infos := make([]fs.FileInfo, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		info, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrBadInput, p, err)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// walkRoot appends every entry of one input, rejecting links and the excluded
// carrier/output as it goes.
func walkRoot(r root, excluded []fs.FileInfo, out *[]entry) error {
	return filepath.WalkDir(r.path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("%w: %q: %w", ErrBadInput, p, err)
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("%w: %q: %w", ErrBadInput, p, err)
		}
		if err := checkType(info); err != nil {
			return fmt.Errorf("%w: %q", err, p)
		}
		for _, ex := range excluded {
			if os.SameFile(info, ex) {
				return fmt.Errorf("%w: %q", ErrCarrierInside, p)
			}
		}

		rel, err := filepath.Rel(r.path, p)
		if err != nil {
			return fmt.Errorf("%w: %q: %w", ErrBadInput, p, err)
		}
		name := r.name
		if rel != "." {
			name += "/" + filepath.ToSlash(rel)
		}

		*out = append(*out, entry{name: name, path: p, info: info, dir: info.IsDir()})
		return nil
	})
}

// writeTar serialises entries with the metadata rules of §5.1: uid/gid 0, no
// owners, zero times, permission bits only.
func writeTar(entries []entry) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, e := range entries {
		hdr := &tar.Header{
			Name:       e.name,
			Mode:       int64(e.info.Mode().Perm()),
			Uid:        0,
			Gid:        0,
			Uname:      "",
			Gname:      "",
			ModTime:    time.Time{},
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
		}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
			hdr.Name = strings.TrimSuffix(hdr.Name, "/") + "/"
			hdr.Size = 0
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = e.info.Size()
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("archive: tar header %q: %w", hdr.Name, err)
		}
		if e.dir {
			continue
		}
		if err := writeFile(tw, e); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("archive: close tar: %w", err)
	}
	return buf.Bytes(), nil
}

func writeFile(tw *tar.Writer, e entry) error {
	f, err := os.Open(e.path)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrBadInput, e.path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.CopyN(tw, f, e.info.Size()); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrBadInput, e.path, err)
	}
	return nil
}

// fit picks between the compressed and the raw tar and enforces maxBytes.
//
// Compression aborts as soon as its output passes maxBytes, since zstd output
// only grows from there; the raw tar is then the fallback. Compression is used
// only when it is actually smaller, which is the "skip compress if not smaller"
// rule of §5.1.
func fit(tarData []byte, maxBytes int64) (Packed, error) {
	compressed, compressedOK := compress(tarData, maxBytes)
	if compressedOK && len(compressed) < len(tarData) {
		return Packed{Data: compressed, Compressed: true}, nil
	}

	if maxBytes > 0 && int64(len(tarData)) > maxBytes {
		return Packed{}, fmt.Errorf("%w: payload is at least %d bytes, limit is %d",
			ErrTooLarge, len(tarData), maxBytes)
	}
	return Packed{Data: tarData}, nil
}

// compress returns zstd(tarData), or false when it cannot be produced within
// maxBytes. Concurrency is pinned to 1 so the bytes do not depend on how many
// CPUs the machine has, which keeps two Packs of the same input identical.
func compress(tarData []byte, maxBytes int64) ([]byte, bool) {
	var buf bytes.Buffer
	lw := &limitWriter{w: &buf, max: maxBytes}

	zw, err := zstd.NewWriter(lw, zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, false
	}
	if _, err := zw.Write(tarData); err != nil {
		_ = zw.Close()
		return nil, false
	}
	if err := zw.Close(); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// limitWriter fails the write that would push the total past max. A max of zero
// or less disables the limit.
type limitWriter struct {
	w   io.Writer
	n   int64
	max int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.max > 0 && l.n+int64(len(p)) > l.max {
		return 0, ErrTooLarge
	}
	n, err := l.w.Write(p)
	l.n += int64(n)
	return n, err
}
