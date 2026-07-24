// Package atomicfile writes a file atomically: temp file in the same dir, fsync,
// rename over the target, best-effort parent-dir fsync. Readers/reboots see either
// the old or the new file, never a truncated one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write atomically writes data to path (0600 file under a 0700 dir). It creates a
// uniquely-named temp file in path's directory, writes+fsyncs it, renames it over
// path, then best-effort fsyncs the directory for rename durability. On any error the
// temp file is removed.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".atomic-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return werr
	}
	if serr := tmp.Sync(); serr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return serr
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		return cerr
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		os.Remove(tmpName)
		return rerr
	}
	if dh, derr := os.Open(dir); derr == nil {
		_ = dh.Sync()
		dh.Close()
	}
	return nil
}
