// Package agentgit is the git plumbing for agent directories: opening or
// initializing the repository, committing with provenance trailers, reverting,
// and reading history.
package agentgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/shell"
)

var (
	// ErrBusy reports that the enclosing repository is mid merge, rebase,
	// cherry-pick or bisect.
	ErrBusy = errors.New("agentgit: repository is busy")
	// ErrConflict reports a revert that conflicted and was aborted.
	ErrConflict = errors.New("agentgit: revert conflicts with later changes")
)

const (
	ModeNested = "nested"
	ModeOuter  = "outer"
)

var argusIdentity = []string{"-c", "user.name=Argus", "-c", "user.email=argus@localhost"}

var busyMarkers = []string{"MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "BISECT_LOG"}

type Repo struct {
	Dir    string // agent directory
	Outer  bool   // committing into an enclosing repository
	GitDir string // .git directory of the repository in use
}

type Trailer struct{ Key, Value string }

type Commit struct {
	Hash, Subject string
	Trailers      map[string]string
	When          time.Time
}

var locks sync.Map // GitDir -> *sync.Mutex

func lock(gitDir string) func() {
	m, _ := locks.LoadOrStore(gitDir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Open prepares the repository for dir. In nested mode dir always has its own
// repository, even inside another one. In outer mode an enclosing repository
// is used when there is one.
func Open(ctx context.Context, dir string, mode string) (*Repo, error) {
	if mode != ModeNested && mode != ModeOuter {
		return nil, fmt.Errorf("agentgit: unknown git mode %q", mode)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, inRepo := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	isRoot := inRepo && samePath(root, dir)
	outer := false
	switch {
	case mode == ModeNested && !isRoot, mode == ModeOuter && !inRepo:
		if _, err := mustGit(ctx, dir, "init", "--quiet"); err != nil {
			return nil, err
		}
	case mode == ModeOuter:
		outer = true
	}
	gitDir, err := mustGit(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	return &Repo{Dir: dir, Outer: outer, GitDir: filepath.Clean(gitDir)}, nil
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// CommitPaths commits changes under paths (relative to Dir) and nothing else.
// It returns "" when nothing changed.
func (r *Repo) CommitPaths(ctx context.Context, subject string, trailers []Trailer, paths []string, argus bool) (string, error) {
	defer lock(r.GitDir)()
	return r.commitPaths(ctx, subject, trailers, paths, argus)
}

func (r *Repo) commitPaths(ctx context.Context, subject string, trailers []Trailer, paths []string, argus bool) (string, error) {
	if r.Outer && r.busy() {
		return "", ErrBusy
	}
	paths = r.knownPaths(ctx, paths)
	if len(paths) == 0 {
		return "", nil
	}
	if _, err := mustGit(ctx, r.Dir, append([]string{"--literal-pathspecs", "add", "--all", "--"}, paths...)...); err != nil {
		return "", err
	}
	if _, ok := runGit(ctx, r.Dir, append([]string{"--literal-pathspecs", "diff", "--cached", "--quiet", "--"}, paths...)...); ok {
		return "", nil
	}
	args := []string{"--literal-pathspecs"}
	if argus {
		args = append(args, argusIdentity...)
	}
	args = append(args, "commit", "--quiet", "--only")
	// The Argus identity cannot sign as the user, and unattended commits must
	// not wait on a passphrase prompt. Hooks always run.
	if argus && !r.Outer {
		args = append(args, "--no-gpg-sign")
	}
	args = append(args, "-m", subject)
	if len(trailers) > 0 {
		var b strings.Builder
		for _, t := range trailers {
			if t.Value != "" {
				fmt.Fprintf(&b, "%s: %s\n", t.Key, strings.Join(strings.Fields(t.Value), " "))
			}
		}
		if b.Len() > 0 {
			args = append(args, "-m", strings.TrimSuffix(b.String(), "\n"))
		}
	}
	args = append(args, "--")
	args = append(args, paths...)
	if _, err := mustGit(ctx, r.Dir, args...); err != nil {
		return "", err
	}
	return mustGit(ctx, r.Dir, "rev-parse", "HEAD")
}

// busy reports whether a merge, rebase, cherry-pick, revert or bisect is in
// progress.
func (r *Repo) busy() bool {
	for _, m := range append([]string{"REVERT_HEAD"}, busyMarkers...) {
		if _, err := os.Lstat(filepath.Join(r.GitDir, m)); err == nil {
			return true
		}
	}
	return false
}

// knownPaths drops paths that neither exist nor are tracked, which git
// would reject as unmatched pathspecs.
func (r *Repo) knownPaths(ctx context.Context, paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Lstat(filepath.Join(r.Dir, p)); err == nil {
			out = append(out, p)
			continue
		}
		if tracked, ok := runGit(ctx, r.Dir, "ls-files", "--", p); ok && tracked != "" {
			out = append(out, p)
		}
	}
	return out
}

// CommitUserEdits commits uncommitted changes under paths as the user, using
// the repository's configured identity when there is one.
func (r *Repo) CommitUserEdits(ctx context.Context, paths []string, agentName, agentID string) (string, error) {
	defer lock(r.GitDir)()
	name, _ := runGit(ctx, r.Dir, "config", "user.name")
	email, _ := runGit(ctx, r.Dir, "config", "user.email")
	return r.commitPaths(ctx, "manual edit", []Trailer{
		{"Argus-Agent", agentName},
		{"Argus-Agent-Id", agentID},
		{"Argus-Source", "you"},
	}, paths, name == "" || email == "")
}

// Revert reverts hash and returns the new commit. A conflicting revert is
// aborted and reported as ErrConflict.
func (r *Repo) Revert(ctx context.Context, hash string) (string, error) {
	defer lock(r.GitDir)()
	if r.busy() {
		return "", ErrBusy
	}
	args := append(append([]string{}, argusIdentity...), "revert", "--no-edit", "--end-of-options", hash)
	if _, err := mustGit(ctx, r.Dir, args...); err != nil {
		if _, statErr := os.Lstat(filepath.Join(r.GitDir, "REVERT_HEAD")); statErr == nil {
			if _, abortErr := mustGit(ctx, r.Dir, "revert", "--abort"); abortErr != nil {
				return "", abortErr
			}
			return "", ErrConflict
		}
		return "", err
	}
	return mustGit(ctx, r.Dir, "rev-parse", "HEAD")
}

const logFormat = "--format=%H%x1f%s%x1f%aI%x1f%(trailers:unfold,only)%x1e"

// Log returns up to limit commits touching paths (all when empty), newest first.
func (r *Repo) Log(ctx context.Context, paths []string, limit int) ([]Commit, error) {
	if _, ok := runGit(ctx, r.Dir, "rev-parse", "--verify", "--quiet", "HEAD"); !ok {
		return nil, nil
	}
	args := []string{"log", logFormat}
	if limit > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", limit))
	}
	args = append(args, "--")
	args = append(args, paths...)
	out, err := mustGit(ctx, r.Dir, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 4)
		if len(f) < 4 {
			continue
		}
		c := Commit{Hash: f[0], Subject: f[1], Trailers: parseTrailers(f[3])}
		c.When, _ = time.Parse(time.RFC3339, f[2])
		commits = append(commits, c)
	}
	return commits, nil
}

func parseTrailers(s string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(ln, ":"); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// BlameSources maps each 1-based line of path to the Argus-Source of the
// commit that last changed it. Uncommitted lines and commits without the
// trailer are omitted.
func (r *Repo) BlameSources(ctx context.Context, path string) (map[int]string, error) {
	out, err := mustGit(ctx, r.Dir, "blame", "--porcelain", "--", path)
	if err != nil {
		return nil, err
	}
	sources := map[string]string{}
	result := map[int]string{}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 3 || strings.HasPrefix(ln, "\t") || (len(f[0]) != 40 && len(f[0]) != 64) {
			continue
		}
		var line int
		if _, err := fmt.Sscanf(f[2], "%d", &line); err != nil {
			continue
		}
		src, seen := sources[f[0]]
		if !seen {
			trailers, err := mustGit(ctx, r.Dir, "log", "-1", "--format=%(trailers:key=Argus-Source,valueonly,unfold)", "--end-of-options", f[0])
			if err != nil {
				// Uncommitted lines have no commit object.
				trailers = ""
			}
			src = strings.TrimSpace(trailers)
			sources[f[0]] = src
		}
		if src != "" {
			result[line] = src
		}
	}
	return result, nil
}

// EnsureExcluded appends patterns not already present to the repository's
// ignore file, never replacing existing content.
func (r *Repo) EnsureExcluded(ctx context.Context, patterns []string) error {
	file := filepath.Join(r.Dir, ".gitignore")
	if r.Outer {
		file = filepath.Join(r.GitDir, "info", "exclude")
	}
	existing, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	have := map[string]bool{}
	for _, ln := range strings.Split(string(existing), "\n") {
		have[strings.TrimSpace(ln)] = true
	}
	var add strings.Builder
	for _, p := range patterns {
		if !have[p] {
			add.WriteString(p + "\n")
			have[p] = true
		}
	}
	if add.Len() == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(add.String())
	return err
}

func runGit(ctx context.Context, dir string, args ...string) (string, bool) {
	cmd := shell.NewCommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return cmd.StdOut().TrimSpace().String(), true
}

func mustGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := shell.NewCommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if err := cmd.Run(); err != nil {
		if msg := gitMessage(cmd.StdErr().String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return cmd.StdOut().TrimSpace().String(), nil
}

// gitMessage reduces git's stderr to its fatal: and error: lines, or the last
// line when there are none.
func gitMessage(stderr string) string {
	var failures []string
	last := ""
	for _, ln := range strings.Split(stderr, "\n") {
		if ln = strings.TrimSpace(ln); ln == "" {
			continue
		}
		last = ln
		for _, prefix := range []string{"fatal: ", "error: "} {
			if rest, ok := strings.CutPrefix(ln, prefix); ok {
				failures = append(failures, rest)
				break
			}
		}
	}
	if len(failures) > 0 {
		return strings.Join(failures, "; ")
	}
	return last
}
