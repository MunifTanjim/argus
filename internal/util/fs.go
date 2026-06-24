package util

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"time"
)

func DirExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return true, nil
		}
		return false, fs.ErrInvalid
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func EnsureDirExists(path string) error {
	if exists, err := DirExists(path); err != nil {
		return err
	} else if !exists {
		return os.MkdirAll(path, 0755)
	}
	return nil
}

func FileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.Mode().IsRegular() {
			return true, nil
		}
		return false, fs.ErrInvalid
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func EnsureFileExists(fpath string) error {
	if err := EnsureDirExists(path.Dir(fpath)); err != nil {
		return err
	}
	if exists, err := FileExists(fpath); err != nil {
		return err
	} else if !exists {
		file, err := os.OpenFile(fpath, os.O_RDONLY|os.O_CREATE, 0644)
		if err != nil {
			return err
		}
		return file.Close()
	}
	return nil
}

func IsFileModifiedWithin(path string, duration time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	modTime := info.ModTime()
	return time.Since(modTime) <= duration
}
