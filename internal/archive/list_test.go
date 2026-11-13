package archive_test

import (
	"archive/tar"
	"errors"
	"path/filepath"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
)

func TestListReportsEntries(t *testing.T) {
	payload := buildTar(t,
		spec{hdr: tar.Header{Name: "project", Typeflag: tar.TypeDir, Mode: 0o755}},
		spec{hdr: tar.Header{Name: "project/a.txt", Typeflag: tar.TypeReg, Mode: 0o644},
			data: []byte("alpha")},
		spec{hdr: tar.Header{Name: "project/nested/b.txt", Typeflag: tar.TypeReg, Mode: 0o644},
			data: []byte("beta gamma")},
	)

	entries, err := archive.List(payload, archive.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []archive.Entry{
		{Name: "project", Dir: true},
		{Name: "project/a.txt", Size: 5},
		{Name: "project/nested/b.txt", Size: 10},
	}
	if len(entries) != len(want) {
		t.Fatalf("List returned %d entries, want %d: %v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i] != w {
			t.Errorf("entry %d = %+v, want %+v", i, entries[i], w)
		}
	}
}

func TestListMatchesPack(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"a.txt", "nested/b.txt"} {
		write(t, filepath.Join(src, name), []byte("content of "+name))
	}

	packed, err := archive.Pack([]string{src}, archive.Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}

	entries, err := archive.List(packed.Data, archive.ListOptions{Compressed: packed.Compressed})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	root := filepath.Base(src)
	want := []archive.Entry{
		{Name: root, Dir: true},
		{Name: root + "/a.txt", Size: int64(len("content of a.txt"))},
		{Name: root + "/nested", Dir: true},
		{Name: root + "/nested/b.txt", Size: int64(len("content of nested/b.txt"))},
	}
	if len(entries) != len(want) {
		t.Fatalf("List returned %d entries, want %d: %v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i] != w {
			t.Errorf("entry %d = %+v, want %+v", i, entries[i], w)
		}
	}
}

func TestListRefusesUnsafePath(t *testing.T) {
	payload := buildTar(t, spec{
		hdr:  tar.Header{Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644},
		data: []byte("pwned"),
	})

	_, err := archive.List(payload, archive.ListOptions{})
	if !errors.Is(err, archive.ErrUnsafePath) {
		t.Fatalf("List error = %v, want ErrUnsafePath", err)
	}
}

func TestListRefusesOversize(t *testing.T) {
	payload := buildTar(t, spec{
		hdr:  tar.Header{Name: "big.bin", Typeflag: tar.TypeReg, Mode: 0o644},
		data: make([]byte, 1024),
	})

	_, err := archive.List(payload, archive.ListOptions{MaxBytes: 512})
	if !errors.Is(err, archive.ErrTooLarge) {
		t.Fatalf("List error = %v, want ErrTooLarge", err)
	}
}
