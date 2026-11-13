//go:build unix

package archive_test

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/a-axa/wisanggeni/internal/archive"
)

func TestPackSpecialFileInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	if _, err := archive.Pack([]string{fifo}, archive.Options{}); !errors.Is(err, archive.ErrUnsupported) {
		t.Fatalf("Pack(FIFO) = %v, want ErrUnsupported", err)
	}
}
