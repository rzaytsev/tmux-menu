package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadBoundsFilesAndRejectsFIFO(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(32 << 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := Read(path, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("large file: %v", err)
	}
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(fifo, 1024); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("FIFO: %v", err)
	}
}

func TestReadKeepsRegularSymlinkAndExactLimit(t *testing.T) {
	root := t.TempDir()
	path, link := filepath.Join(root, "file"), filepath.Join(root, "link")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	got, err := Read(link, 4)
	if err != nil || string(got) != "data" {
		t.Fatalf("symlink at limit: %q, %v", got, err)
	}
}
