package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/gittree"
)

func copyIncluded(ctx context.Context, mainDir, dir string, out io.Writer) bool {
	files, err := gittree.IncludedFiles(ctx, mainDir)
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		fmt.Fprintf(out, ".worktreeinclude: %s\n", err)
		return false
	}
	copied := 0
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		if err := copyNew(mainDir, dir, f); err != nil {
			fmt.Fprintf(out, ".worktreeinclude: %s\n", err)
			continue
		}
		copied++
	}
	if copied == len(files) {
		fmt.Fprintf(out, ".worktreeinclude: copied %d files\n", copied)
		return true
	}
	fmt.Fprintf(out, ".worktreeinclude: copied %d of %d files\n", copied, len(files))
	return false
}

// copyNew leaves rel alone when it already exists in dstRoot. dstRoot must
// have no symlinks in its path.
func copyNew(srcRoot, dstRoot, rel string) error {
	src, dst := filepath.Join(srcRoot, rel), filepath.Join(dstRoot, rel)
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	if err := mkdirLike(srcRoot, dstRoot, filepath.Dir(rel)); err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", rel)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		// OpenFile's mode is filtered by the umask.
		err = out.Chmod(fi.Mode().Perm())
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// mkdirLike creates the missing directories of rel under dstRoot with the
// modes of the same directories under srcRoot. It refuses a directory that
// resolves outside dstRoot, which a symlink on the checked-out branch can do.
func mkdirLike(srcRoot, dstRoot, rel string) error {
	if rel == "." {
		return nil
	}
	dst := filepath.Join(dstRoot, rel)
	if _, err := os.Lstat(dst); err == nil {
		real, err := filepath.EvalSymlinks(dst)
		if err != nil {
			return err
		}
		if !within(dstRoot, real) {
			return fmt.Errorf("%s: outside the worktree", rel)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := mkdirLike(srcRoot, dstRoot, filepath.Dir(rel)); err != nil {
		return err
	}
	fi, err := os.Stat(filepath.Join(srcRoot, rel))
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	// The owner keeps rwx so that the files under it can still be written.
	return os.Chmod(dst, fi.Mode().Perm()|0o700)
}
