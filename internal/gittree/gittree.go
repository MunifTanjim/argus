// Package gittree resolves a directory to its git repository and worktree, and
// enumerates a repository's worktrees.
package gittree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
)

var ErrNotRepo = errors.New("gittree: not a git repository")

type Location struct {
	GitDir       string // absolute common git directory; the repository identity
	WorktreeRoot string // absolute worktree working directory ("" for a bare repo)
	Branch       string // current branch; "" when HEAD is detached
	Head         string // short HEAD commit SHA
	IsMain       bool   // whether this worktree is the repository's main working tree
}

// Worktree is one entry from `git worktree list`.
type Worktree struct {
	Dir      string
	Head     string // short HEAD commit SHA
	Branch   string // "" when detached or bare
	Bare     bool
	Detached bool
	IsMain   bool // the repository's main working tree
}

// Resolve returns ErrNotRepo when dir is not inside a git repository.
func Resolve(ctx context.Context, dir string) (Location, error) {
	commonDir, ok := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if !ok {
		return Location{}, ErrNotRepo
	}
	loc := Location{GitDir: filepath.Clean(commonDir)}
	if gitDir, ok := git(ctx, dir, "rev-parse", "--absolute-git-dir"); ok {
		loc.IsMain = filepath.Clean(gitDir) == loc.GitDir
	}
	if root, ok := git(ctx, dir, "rev-parse", "--show-toplevel"); ok {
		loc.WorktreeRoot = filepath.Clean(root)
	}
	loc.Branch, _ = git(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	loc.Head, _ = git(ctx, dir, "rev-parse", "--short", "HEAD")
	return loc, nil
}

// ListWorktrees returns every worktree of the repository with the given common
// git directory.
func ListWorktrees(ctx context.Context, gitDir string) ([]Worktree, error) {
	cmd := shell.NewCommandContext(ctx, "git", "--git-dir="+gitDir, "worktree", "list", "--porcelain")
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return parseWorktrees(cmd.StdOut().String()), nil
}

// parseWorktrees parses `git worktree list --porcelain`: blank-line-separated
// records of "worktree <path>", "HEAD <sha>", "branch <ref>", "detached", or
// "bare". Git lists the main working tree first.
func parseWorktrees(out string) []Worktree {
	var (
		res  []Worktree
		cur  Worktree
		open bool
	)
	flush := func() {
		if open {
			res = append(res, cur)
		}
		cur = Worktree{}
		open = false
	}
	firstNonBare := true
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			cur.Dir = filepath.Clean(val)
			open = true
		case "HEAD":
			if len(val) >= 7 {
				cur.Head = val[:7]
			} else {
				cur.Head = val
			}
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "detached":
			cur.Detached = true
		case "bare":
			cur.Bare = true
		}
	}
	flush()
	for i := range res {
		if !res[i].Bare && firstNonBare {
			res[i].IsMain = true
			firstNonBare = false
		}
	}
	return res
}

// AddWorktree creates a worktree at path on branch. An existing branch is checked
// out as is; a new one is created from start (empty = current HEAD). repoDir is
// any working tree of the repository.
func AddWorktree(ctx context.Context, repoDir, path, branch, start string) error {
	if _, ok := git(ctx, repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); ok {
		return runGit(ctx, "worktree add", "-C", repoDir, "worktree", "add", path, branch)
	}
	args := []string{"-C", repoDir, "worktree", "add", "-b", branch, path}
	if start != "" {
		args = append(args, start)
	}
	return runGit(ctx, "worktree add", args...)
}

// RemoveWorktree refuses a dirty worktree unless force is set.
func RemoveWorktree(ctx context.Context, repoDir, path string, force bool) error {
	args := []string{"-C", repoDir, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	return runGit(ctx, "worktree remove", args...)
}

// DefaultBranch resolves the repository's default branch: origin/HEAD, then main,
// then master, then the current branch. Empty when none resolves.
func DefaultBranch(ctx context.Context, repoDir string) string {
	if r, ok := git(ctx, repoDir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ok {
		return strings.TrimPrefix(r, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if _, ok := git(ctx, repoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); ok {
			return b
		}
	}
	if b, ok := git(ctx, repoDir, "symbolic-ref", "--quiet", "--short", "HEAD"); ok {
		return b
	}
	return ""
}

func runGit(ctx context.Context, what string, args ...string) error {
	cmd := shell.NewCommandContext(ctx, "git", args...)
	if err := cmd.Run(); err != nil {
		if msg := cmd.StdErr().TrimSpace().String(); msg != "" {
			return fmt.Errorf("gittree: %s: %s", what, msg)
		}
		return fmt.Errorf("gittree: %s: %w", what, err)
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := shell.NewCommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return cmd.StdOut().TrimSpace().String(), true
}

// RepoName names the repository containing dir by its common git dir, so a
// linked worktree resolves to its main repository. Outside a repository it is
// dir's own base name. It reads files only (no git process), because discovery
// calls it on every scan.
func RepoName(dir string) string {
	if dir == "" {
		return ""
	}
	for d := dir; ; {
		gitPath := filepath.Join(d, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if !fi.IsDir() {
				if common := worktreeCommonDir(gitPath); common != "" {
					return NameFromGitDir(common)
				}
			}
			return filepath.Base(d)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return filepath.Base(dir)
		}
		d = parent
	}
}

// NameFromGitDir names a repository from its common git dir: the parent of a
// ".git" directory, or a bare "name.git" without the suffix.
func NameFromGitDir(gitDir string) string {
	gitDir = filepath.Clean(gitDir)
	if filepath.Base(gitDir) == ".git" {
		return filepath.Base(filepath.Dir(gitDir))
	}
	return strings.TrimSuffix(filepath.Base(gitDir), ".git")
}

// worktreeCommonDir follows a linked worktree's .git file ("gitdir: <path>") to
// the worktree's git dir, whose commondir file points at the repository's
// common git dir. It returns "" for a .git file with no commondir (a submodule).
func worktreeCommonDir(gitFile string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(gitFile), gitdir)
	}
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	return filepath.Clean(common)
}
