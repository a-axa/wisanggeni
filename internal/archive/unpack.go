package archive

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// DefaultMaxBytes is the decompressed-size ceiling Unpack applies when the
// caller does not set one. It matches the CLI default for `reveal --max-output`
// (§5): a payload that expands past this is refused instead of filling the disk.
const DefaultMaxBytes int64 = 1 << 30

var (
	// ErrBadArchive reports a payload that is not a readable tar (or zstd)
	// stream, or that is internally inconsistent.
	ErrBadArchive = errors.New("archive: malformed payload")

	// ErrUnsafePath reports an entry name that would escape the output
	// directory, such as an absolute path or one containing ".." (§7.9).
	ErrUnsafePath = errors.New("archive: unsafe entry path")

	// ErrExists reports a destination that already exists while Force is off.
	ErrExists = errors.New("archive: destination already exists")
)

// UnpackOptions controls Unpack.
type UnpackOptions struct {
	// Compressed says the payload is zstd, i.e. header_plain.flags bit 0.
	Compressed bool

	// MaxBytes caps the decompressed size. Zero means DefaultMaxBytes. A
	// negative value is also treated as DefaultMaxBytes, for the reason given
	// on ListOptions.MaxBytes.
	MaxBytes int64

	// Force allows replacing existing files.
	Force bool
}

// Entry describes one archive member.
type Entry struct {
	// Name is the cleaned, slash-separated path relative to the output
	// directory.
	Name string

	// Size is the uncompressed byte length, zero for directories.
	Size int64

	// Dir reports a directory entry.
	Dir bool
}

// Unpacked reports what Unpack wrote.
type Unpacked struct {
	// Entries lists the members in archive order.
	Entries []Entry

	// Paths lists the absolute paths written, in the same order as Entries.
	Paths []string
}

// Unpack extracts a tar payload into dir, refusing anything that could write
// outside it (§7.9 step 10).
//
// Every header is validated before a single byte is written: names must stay
// local, only regular files and directories are allowed, and duplicate targets
// are rejected. Symlinked parents inside dir are refused, so a pre-existing
// link cannot be used to redirect a write. Permission bits are masked to 0o777,
// dropping setuid and setgid.
func Unpack(data []byte, dir string, opts UnpackOptions) (Unpacked, error) {
	maxBytes := capBytes(opts.MaxBytes)

	payload, err := decodePayload(data, opts.Compressed, maxBytes)
	if err != nil {
		return Unpacked{}, err
	}

	items, err := planArchive(payload, maxBytes)
	if err != nil {
		return Unpacked{}, err
	}

	base, err := filepath.Abs(dir)
	if err != nil {
		return Unpacked{}, fmt.Errorf("archive: %q: %w", dir, err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return Unpacked{}, fmt.Errorf("archive: %q: %w", dir, err)
	}

	targets := make([]string, len(items))
	for i, it := range items {
		target, err := joinSafe(base, it.rel)
		if err != nil {
			return Unpacked{}, err
		}
		targets[i] = target
	}

	// Pre-flight: surface a refusal before touching the filesystem, so a
	// rejected archive leaves no half-written files behind.
	if err := preflight(items, targets, opts.Force); err != nil {
		return Unpacked{}, err
	}

	return extract(payload, items, targets, base, opts.Force)
}

// decodePayload turns the container body into raw tar bytes and enforces the
// size cap. When the payload is zstd, the reader itself is limited so a
// decompression bomb stops early instead of allocating past maxBytes.
func decodePayload(data []byte, compressed bool, maxBytes int64) ([]byte, error) {
	if !compressed {
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("%w: %d bytes, limit is %d", ErrTooLarge, len(data), maxBytes)
		}
		return data, nil
	}

	zr, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: zstd: %w", ErrBadArchive, err)
	}
	defer zr.Close()

	var src io.Reader = zr
	if maxBytes > 0 {
		// Read one byte past the cap: if it arrives, the payload is too big.
		src = io.LimitReader(zr, maxBytes+1)
	}
	out, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("%w: zstd: %w", ErrBadArchive, err)
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("%w: decompressed output exceeds %d bytes", ErrTooLarge, maxBytes)
	}
	return out, nil
}

// planned is one validated entry, ready to be written.
type planned struct {
	Entry

	raw  string      // header name as stored, for the lockstep check
	rel  string      // cleaned, slash-separated path relative to the output dir
	mode fs.FileMode // permission bits only, setuid/setgid already stripped
}

// planArchive reads every header and validates it without touching the disk.
func planArchive(data []byte, maxBytes int64) ([]planned, error) {
	tr := tar.NewReader(bytes.NewReader(data))
	seen := make(map[string]bool)
	var (
		items []planned
		total int64
	)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBadArchive, err)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir:
			// allowed; the reader folds the legacy '\x00' type into TypeReg
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("%w: %q is a link", ErrUnsupported, hdr.Name)
		case tar.TypeChar, tar.TypeBlock:
			return nil, fmt.Errorf("%w: %q is a device node", ErrUnsupported, hdr.Name)
		case tar.TypeFifo:
			return nil, fmt.Errorf("%w: %q is a FIFO", ErrUnsupported, hdr.Name)
		default:
			return nil, fmt.Errorf("%w: %q has type %q", ErrUnsupported, hdr.Name, hdr.Typeflag)
		}

		rel, err := safeRel(hdr.Name)
		if err != nil {
			return nil, err
		}
		if seen[rel] {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrBadArchive, hdr.Name)
		}
		seen[rel] = true

		isDir := hdr.Typeflag == tar.TypeDir
		if hdr.Size < 0 {
			return nil, fmt.Errorf("%w: %q has a negative size", ErrBadArchive, hdr.Name)
		}
		if !isDir {
			// Compare against the remaining budget rather than adding, so a
			// huge claimed size cannot wrap int64.
			if maxBytes > 0 && hdr.Size > maxBytes-total {
				return nil, fmt.Errorf("%w: entries total exceeds %d bytes", ErrTooLarge, maxBytes)
			}
			total += hdr.Size
		}

		items = append(items, planned{
			Entry: Entry{Name: rel, Size: hdr.Size, Dir: isDir},
			raw:   hdr.Name,
			rel:   rel,
			mode:  fs.FileMode(hdr.Mode).Perm(),
		})
	}
	return items, nil
}

// safeRel validates an entry name and returns it cleaned and slash-separated.
// It rejects absolute paths, "..", NUL bytes and anything filepath.IsLocal
// rejects. Backslashes are treated as separators on Windows only, so a Unix
// name that merely contains a backslash keeps its meaning there.
func safeRel(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}

	n := name
	if filepath.Separator == '\\' {
		n = strings.ReplaceAll(n, `\`, "/")
	}
	n = path.Clean(n)

	if n == "." || n == ".." ||
		strings.HasPrefix(n, "../") ||
		strings.HasPrefix(n, "/") {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	if !filepath.IsLocal(filepath.FromSlash(n)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	return n, nil
}

// joinSafe joins a validated relative path onto base and re-checks that the
// result is still inside base.
func joinSafe(base, rel string) (string, error) {
	target := filepath.Join(base, filepath.FromSlash(rel))
	r, err := filepath.Rel(base, target)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, rel)
	}
	return target, nil
}

// preflight reports the first destination that cannot be written, before any
// write happens.
func preflight(items []planned, targets []string, force bool) error {
	for i, it := range items {
		info, err := os.Lstat(targets[i])
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("archive: %q: %w", targets[i], err)
		case !force:
			return fmt.Errorf("%w: %q", ErrExists, targets[i])
		case it.Dir && !info.IsDir():
			return fmt.Errorf("%w: %q is not a directory", ErrExists, targets[i])
		case !it.Dir && info.IsDir():
			return fmt.Errorf("%w: %q is a directory", ErrExists, targets[i])
		}
	}
	return nil
}

// extract writes the validated entries. It re-reads the tar so file contents
// stream straight to disk; the plan and the stream are walked in lockstep.
func extract(payload []byte, items []planned, targets []string, base string, force bool) (Unpacked, error) {
	result := Unpacked{
		Entries: make([]Entry, 0, len(items)),
		Paths:   make([]string, 0, len(items)),
	}
	var dirs []int // indexes of directory entries, perms restored at the end

	tr := tar.NewReader(bytes.NewReader(payload))
	for i, it := range items {
		hdr, err := tr.Next()
		if err != nil {
			return Unpacked{}, fmt.Errorf("%w: %w", ErrBadArchive, err)
		}
		if hdr.Name != it.raw {
			return Unpacked{}, fmt.Errorf("%w: entry order changed", ErrBadArchive)
		}

		if it.Dir {
			if err := ensureDir(base, it.rel); err != nil {
				return Unpacked{}, err
			}
			dirs = append(dirs, i)
		} else {
			parent := path.Dir(it.rel)
			if parent != "." {
				if err := ensureDir(base, parent); err != nil {
					return Unpacked{}, err
				}
			}
			if err := writeEntry(tr, targets[i], it.mode, hdr.Size, force); err != nil {
				return Unpacked{}, err
			}
		}

		result.Entries = append(result.Entries, it.Entry)
		result.Paths = append(result.Paths, targets[i])
	}

	// Restore directory modes last: a read-only directory would otherwise
	// block writing its children.
	for _, i := range dirs {
		if err := os.Chmod(targets[i], items[i].mode); err != nil {
			return Unpacked{}, fmt.Errorf("archive: %q: %w", targets[i], err)
		}
	}
	return result, nil
}

// ensureDir creates rel under base one component at a time, refusing to follow
// a symlink.
func ensureDir(base, rel string) error {
	cur := base
	for _, comp := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, comp)

		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(cur, 0o755); err != nil {
				return fmt.Errorf("archive: %q: %w", cur, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("archive: %q: %w", cur, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %q is a symlink", ErrUnsafePath, cur)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: %q is not a directory", ErrUnsafePath, cur)
		}
	}
	return nil
}

// writeEntry creates one regular file and copies exactly size bytes into it.
// O_EXCL means an existing path, including a symlink, is never written through;
// with force the old file is removed first.
func writeEntry(tr *tar.Reader, target string, mode fs.FileMode, size int64, force bool) error {
	if force {
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("archive: %q: %w", target, err)
		}
	}

	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q", ErrExists, target)
	}
	if err != nil {
		return fmt.Errorf("archive: %q: %w", target, err)
	}

	if _, err := io.CopyN(f, tr, size); err != nil {
		_ = f.Close()
		return fmt.Errorf("%w: %q: %w", ErrBadArchive, target, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("archive: %q: %w", target, err)
	}
	if err := os.Chmod(target, mode); err != nil {
		return fmt.Errorf("archive: %q: %w", target, err)
	}
	return nil
}
