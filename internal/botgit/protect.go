package botgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Protected fingerprints the repository files that decide what git runs when
// Argus commits: the config and every file in the hooks directory (which
// core.hooksPath may move). Keys name each file relative to Dir; values are
// hashes of its mode and content. It runs no hooks.
func (r *Repo) Protected(ctx context.Context) (map[string]string, error) {
	common := r.GitDir
	if data, err := os.ReadFile(filepath.Join(r.GitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(data))
		if !filepath.IsAbs(c) {
			c = filepath.Join(r.GitDir, c)
		}
		common = filepath.Clean(c)
	}
	out := map[string]string{}
	for _, f := range []string{filepath.Join(common, "config"), filepath.Join(r.GitDir, "config.worktree")} {
		if h, ok := hashFile(f); ok {
			out[r.rel(f)] = h
		}
	}
	hooks, err := mustGit(ctx, r.Dir, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(r.Dir, hooks)
	}
	err = filepath.WalkDir(hooks, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if path == hooks && os.IsNotExist(err) {
				return nil
			}
			out[r.rel(path)] = "unreadable: " + err.Error()
			return nil
		}
		if e.IsDir() {
			return nil
		}
		h, _ := hashFile(path)
		out[r.rel(path)] = h
		return nil
	})
	return out, err
}

// rel names path relative to Dir; git reports the .git directory with
// symlinks resolved, so the resolved Dir is tried as well.
func (r *Repo) rel(path string) string {
	rel, err := filepath.Rel(r.Dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		if dir, derr := filepath.EvalSymlinks(r.Dir); derr == nil {
			if alt, aerr := filepath.Rel(dir, path); aerr == nil {
				rel, err = alt, nil
			}
		}
	}
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// hashFile hashes path's mode, symlink target and content. ok is false when
// it does not exist.
func hashFile(path string) (string, bool) {
	h := sha256.New()
	if target, err := os.Readlink(path); err == nil {
		fmt.Fprintf(h, "link %s\n", target)
	}
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		if _, lerr := os.Lstat(path); lerr != nil {
			return "", false
		}
	}
	if err == nil {
		fmt.Fprintf(h, "mode %o\n", fi.Mode())
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(h, "unreadable %v\n", err)
		}
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// ProtectedChanges lists what differs from old to cur, one entry per name:
// "<name> (added)", "<name> (removed)" or "<name> (changed)", sorted.
func ProtectedChanges(old, cur map[string]string) []string {
	var out []string
	for k, v := range cur {
		switch ov, ok := old[k]; {
		case !ok:
			out = append(out, k+" (added)")
		case ov != v:
			out = append(out, k+" (changed)")
		}
	}
	for k := range old {
		if _, ok := cur[k]; !ok {
			out = append(out, k+" (removed)")
		}
	}
	slices.Sort(out)
	return out
}
