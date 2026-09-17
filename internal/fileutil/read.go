// Package fileutil bounds reads of project-controlled files on tmux's Unix hosts.
package fileutil

import (
	"errors"
	"io"
	"os"
	"syscall"
)

var ErrTooLarge = errors.New("file exceeds size limit")
var ErrNotRegular = errors.New("not a regular file")

// OpenRegular avoids blocking on FIFOs, including a file replaced during a scan.
// Symlinks to regular files remain supported for configuration files.
func OpenRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, ErrNotRegular
	}
	return f, nil
}

func Read(path string, limit int64) ([]byte, error) {
	f, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if limit < 0 || info.Size() > limit {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, err
}
