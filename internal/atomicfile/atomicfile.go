// Package atomicfile writes a file atomically: temp file in the same dir, fsync,
// rename (or link) to the target, best-effort parent-dir fsync. Readers/reboots
// see either the old or the new file, never a truncated one.
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Write atomically writes data to path (0600 file under a 0700 dir). The
// directory fsync makes the rename durable across a crash.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpName, err := writeTemp(dir, data, 0o600)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	syncDir(dir)
	return nil
}

// Replace atomically writes data to path in place of its current content. A
// symlinked path stays a symlink; its target is replaced. An existing file keeps
// its permissions (0600 for a new file). Replace never creates directories.
func Replace(path string, data []byte) error {
	target, err := filepath.EvalSymlinks(path)
	mode := os.FileMode(0o600)
	switch {
	case err == nil:
		fi, err := os.Stat(target)
		if err != nil {
			return err
		}
		mode = fi.Mode().Perm()
	case errors.Is(err, fs.ErrNotExist):
		if _, lerr := os.Lstat(path); lerr == nil {
			return fmt.Errorf("%s: dangling symlink", path)
		}
		target = path
	default:
		return err
	}
	dir := filepath.Dir(target)
	tmpName, err := writeTemp(dir, data, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return err
	}
	syncDir(dir)
	return nil
}

// Create writes data to path (0600 file under a 0700 dir) only if path does not
// exist, returning an error matching fs.ErrExist otherwise. Concurrent creators
// agree on one winner, and readers never see a partial file.
func Create(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmpName, err := writeTemp(dir, data, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)
	if err := os.Link(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// writeTemp writes data, fsynced, to a new temp file in dir with mode and
// returns its name. On error the temp file is gone.
func writeTemp(dir string, data []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(dir, ".atomic-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// syncDir makes a rename or link in dir durable, best effort.
func syncDir(dir string) {
	if dh, err := os.Open(dir); err == nil {
		_ = dh.Sync()
		dh.Close()
	}
}
