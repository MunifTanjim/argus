// Package logfile provides a size-limited log file that keeps one previous file.
package logfile

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type File struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	size       int64
	file       *os.File
	redirected bool
}

// Open moves an existing file at path to path+".1", replacing it, and opens a new one.
func Open(path string, maxSize int64) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f := &File{path: path, maxSize: maxSize}
	if err := f.rotate(); err != nil {
		return nil, err
	}
	return f, nil
}

// Write rotates first when p would push the file past maxSize. A write larger
// than maxSize goes whole into the new file. A failed rotation still writes p
// to the current file and reports the rotation error.
func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rerr error
	if f.size > 0 && f.size+int64(len(p)) > f.maxSize {
		rerr = f.rotate()
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	return n, errors.Join(err, rerr)
}

// RedirectStd dups the current file onto fd 1 and 2; later rotations dup again.
func (f *File) RedirectStd() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.dupStd(); err != nil {
		return err
	}
	f.redirected = true
	return nil
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.file.Close()
}

// rotate keeps the current file open until the new one is, so a failure
// leaves a working handle.
func (f *File) rotate() error {
	if err := os.Rename(f.path, f.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	nf, err := os.OpenFile(f.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if f.file != nil {
		_ = f.file.Close()
	}
	f.file = nf
	f.size = 0
	if f.redirected {
		return f.dupStd()
	}
	return nil
}

func (f *File) dupStd() error {
	fd := int(f.file.Fd())
	if err := dupOnto(fd, 1); err != nil {
		return err
	}
	return dupOnto(fd, 2)
}
