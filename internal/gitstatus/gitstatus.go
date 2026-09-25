// Package gitstatus reports a working tree's changes against HEAD and fetches a
// single changed file's HEAD and working-tree content for review from the app.
package gitstatus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
)

// maxContentBytes caps each side of a file returned for review; larger files are
// reported as not-shown rather than shipped over the wire.
const maxContentBytes = 1 << 20 // 1 MiB

// ChangeType is the net change of a file relative to HEAD.
type ChangeType string

const (
	ChangeAdded     ChangeType = "added"
	ChangeModified  ChangeType = "modified"
	ChangeDeleted   ChangeType = "deleted"
	ChangeRenamed   ChangeType = "renamed"
	ChangeUntracked ChangeType = "untracked"
)

// ChangedFile is one entry from `git status`.
type ChangedFile struct {
	Path     string     // current (working-tree) path
	OrigPath string     // rename/copy source (HEAD-side path), else ""
	Change   ChangeType // net change vs HEAD
	Staged   bool       // true when the index differs from HEAD for this path (X)
	Unstaged bool       // true when the working tree differs from the index (Y)
}

// ChangedFiles returns the repo root containing dir and every file that differs
// from HEAD — staged, unstaged, or untracked. It errors when dir is empty or not
// inside a git repository.
func ChangedFiles(ctx context.Context, dir string) (root string, files []ChangedFile, err error) {
	root, err = repoRoot(ctx, dir)
	if err != nil {
		return "", nil, err
	}
	// -z: NUL-terminated records with no path quoting (safe for spaces/unicode).
	// --untracked-files=all: list individual untracked files, not just their dir.
	cmd := shell.NewCommandContext(ctx, "git", "-C", root,
		"status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("gitstatus: git status failed in %s: %w", root, err)
	}
	return root, parsePorcelainZ(cmd.StdOut().String()), nil
}

// parsePorcelainZ parses the NUL-separated output of `git status --porcelain=v1 -z`.
// Each record is "XY PATH"; rename/copy records (X == 'R'/'C') are followed by a
// second token holding the original (HEAD-side) path.
func parsePorcelainZ(out string) []ChangedFile {
	tokens := strings.Split(out, "\x00")
	var files []ChangedFile
	for i := 0; i < len(tokens); i++ {
		rec := tokens[i]
		if len(rec) < 4 { // need at least "XY " + 1-char path; trailing "" is skipped
			continue
		}
		x, y := rec[0], rec[1]
		cf := ChangedFile{
			Path:     rec[3:],
			Staged:   x != ' ' && x != '?',
			Unstaged: y != ' ' && y != '?', // worktree differs from index
		}
		switch {
		case x == '?' && y == '?':
			cf.Change = ChangeUntracked
		case x == 'R' || x == 'C':
			cf.Change = ChangeRenamed
			// The destination path is in this record; the original is the next token.
			if i+1 < len(tokens) {
				cf.OrigPath = tokens[i+1]
				i++
			}
		case x == 'D' || y == 'D':
			cf.Change = ChangeDeleted
		case x == 'A' || y == 'A':
			cf.Change = ChangeAdded
		default:
			cf.Change = ChangeModified
		}
		files = append(files, cf)
	}
	return files
}

// FileContents returns the HEAD version (old) and working-tree version (new) of a
// changed file for rendering a diff. Either side is "" when it does not exist:
// untracked/added → old ""; deleted → new ""; empty repo (no HEAD) → old "".
// notShown is true when either side is binary (contains a NUL byte) or exceeds the
// size cap, in which case both contents are returned empty.
func FileContents(ctx context.Context, dir, path, origPath string) (old, new string, notShown bool, err error) {
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return "", "", false, err
	}
	headPath := path
	if origPath != "" {
		headPath = origPath // rename: HEAD holds the source path
	}
	workPath, err := repoRelPath(root, path)
	if err != nil {
		return "", "", false, err
	}
	// Lstat, not Stat: a changed file may be a symlink. repoRelPath only checks the
	// path lexically, so following a symlink here (via Stat/ReadFile) could read a
	// file outside the repo. Match git instead — a symlink's blob is its target
	// text — by reading the link itself.
	fi, serr := os.Lstat(workPath)
	symlink := serr == nil && fi.Mode()&os.ModeSymlink != 0
	if serr == nil && !symlink && fi.Size() > maxContentBytes {
		return "", "", true, nil // skip reading an oversized working file into memory
	}
	old, oversize, err := gitShowRev(ctx, root, "HEAD", headPath)
	if err != nil {
		return "", "", false, err
	}
	if oversize {
		return "", "", true, nil
	}
	if symlink {
		if target, lerr := os.Readlink(workPath); lerr == nil {
			new = target
		}
	} else if b, rerr := os.ReadFile(workPath); rerr == nil {
		new = string(b)
	}
	if isBinary(old) || isBinary(new) || len(new) > maxContentBytes {
		return "", "", true, nil
	}
	return old, new, false, nil
}

type DirEntry struct {
	Name  string // base name
	Path  string // repo-relative slash path, for the next ListDir/ReadFile
	IsDir bool
}

// ListDir lists the children of rel (a repo-relative slash path; "" is the root),
// hiding .git and git-ignored entries. dir need not be a git repo; a plain
// directory lists without ignore filtering. Directories sort first.
func ListDir(ctx context.Context, dir, rel string) (root string, entries []DirEntry, err error) {
	root = dir
	inRepo := false
	if r, e := repoRoot(ctx, dir); e == nil {
		root, inRepo = r, true
	}
	full, err := repoRelPath(root, rel)
	if err != nil {
		return "", nil, err
	}
	des, err := os.ReadDir(full)
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(des))
	dirOf := make(map[string]bool, len(des))
	for _, de := range des {
		if de.Name() == ".git" {
			continue
		}
		names = append(names, de.Name())
		dirOf[de.Name()] = de.IsDir()
	}
	var ignored map[string]bool
	if inRepo {
		ignored = checkIgnore(ctx, root, full, names)
	}
	for _, n := range names {
		if ignored[n] {
			continue
		}
		entries = append(entries, DirEntry{Name: n, Path: path.Join(rel, n), IsDir: dirOf[n]})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return root, entries, nil
}

// checkIgnore reads matches from stdout, so git's exit code (1 = none ignored)
// is irrelevant.
func checkIgnore(ctx context.Context, root, full string, names []string) map[string]bool {
	var in strings.Builder
	for _, n := range names {
		in.WriteString(filepath.Join(full, n))
		in.WriteByte(0)
	}
	cmd := shell.NewCommandContext(ctx, "git", "-C", root, "check-ignore", "--stdin", "-z").
		WithStdIn(strings.NewReader(in.String()))
	_ = cmd.Run()
	ignored := map[string]bool{}
	for _, p := range strings.Split(cmd.StdOut().String(), "\x00") {
		if p != "" {
			ignored[filepath.Base(p)] = true
		}
	}
	return ignored
}

// ReadFile returns the working-tree content of a repo-relative file. notShown is
// true for a binary or oversized file. A symlink returns its target text (it is
// never followed).
func ReadFile(ctx context.Context, dir, rel string) (content string, notShown bool, err error) {
	root := dir
	if r, e := repoRoot(ctx, dir); e == nil {
		root = r
	}
	full, err := repoRelPath(root, rel)
	if err != nil {
		return "", false, err
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return "", false, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if target, e := os.Readlink(full); e == nil {
			return target, false, nil
		}
		return "", true, nil
	}
	if fi.IsDir() {
		return "", false, fmt.Errorf("gitstatus: is a directory: %s", rel)
	}
	// Reading a FIFO or device can block forever.
	if !fi.Mode().IsRegular() {
		return "", false, fmt.Errorf("gitstatus: not a regular file: %s", rel)
	}
	if fi.Size() > maxContentBytes {
		return "", true, nil
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", false, err
	}
	if isBinary(string(b)) {
		return "", true, nil
	}
	return string(b), false, nil
}

// WorkingDiff returns a unified diff of a repo-relative path against HEAD. An
// untracked (or new-repo) path is rendered as an all-added diff from its
// content. notShown is true for a binary or oversized result.
func WorkingDiff(ctx context.Context, dir, p string) (diff string, notShown bool, err error) {
	return DiffSince(ctx, dir, "HEAD", p, "")
}

// TargetBase returns the merge base of HEAD and target, preferring
// origin/<target> when it exists. It does not fetch.
func TargetBase(ctx context.Context, dir, target string) (string, error) {
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return "", err
	}
	ref := "origin/" + target
	if !revExists(ctx, root, ref) {
		ref = target
	}
	if !revExists(ctx, root, ref) {
		return "", fmt.Errorf("gitstatus: unknown target branch: %s", target)
	}
	base := mergeBase(ctx, root, ref)
	if base == "" {
		return "", fmt.Errorf("gitstatus: no merge base with %s", ref)
	}
	return base, nil
}

// ChangedFilesSince lists files that differ between base and the working tree,
// plus untracked files.
func ChangedFilesSince(ctx context.Context, dir, base string) (root string, files []ChangedFile, err error) {
	root, err = repoRoot(ctx, dir)
	if err != nil {
		return "", nil, err
	}
	cmd := shell.NewCommandContext(ctx, "git", "-C", root, "diff", "--name-status", "-M", "-z", base, "--")
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("gitstatus: git diff failed in %s: %w", root, err)
	}
	files = parseNameStatusZ(cmd.StdOut().String())
	un := shell.NewCommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	if err := un.Run(); err != nil {
		return "", nil, fmt.Errorf("gitstatus: git ls-files failed in %s: %w", root, err)
	}
	for _, p := range strings.Split(un.StdOut().String(), "\x00") {
		if p != "" {
			files = append(files, ChangedFile{Path: p, Change: ChangeUntracked})
		}
	}
	return root, files, nil
}

// DiffSince returns a unified diff of a repo-relative path between base and the
// working tree; origPath (a rename source, or "") lets git show the rename. An
// untracked path is rendered as an all-added diff. notShown is true for a
// binary or oversized result.
func DiffSince(ctx context.Context, dir, base, p, origPath string) (diff string, notShown bool, err error) {
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return "", false, err
	}
	if _, err := repoRelPath(root, p); err != nil {
		return "", false, err
	}
	args := []string{"-C", root, "diff", "--no-color", "-M", base, "--", p}
	if origPath != "" {
		if _, err := repoRelPath(root, origPath); err != nil {
			return "", false, err
		}
		args = append(args, origPath)
	}
	cmd := shell.NewCommandContext(ctx, "git", args...)
	if err := cmd.Run(); err != nil {
		if revExists(ctx, root, base) {
			return "", false, fmt.Errorf("gitstatus: git diff failed in %s: %w", root, err)
		}
		// No base (HEAD in a new repo): every file is new.
		return addedDiff(ctx, dir, p)
	}
	out := cmd.StdOut().String()
	if strings.TrimSpace(out) != "" {
		if len(out) > maxContentBytes || isBinary(out) {
			return "", true, nil
		}
		return out, false, nil
	}
	if tracked(ctx, root, p) {
		return "", false, nil
	}
	return addedDiff(ctx, dir, p)
}

func tracked(ctx context.Context, root, p string) bool {
	cmd := shell.NewCommandContext(ctx, "git", "-C", root, "ls-files", "--error-unmatch", "--", p)
	return cmd.Run() == nil
}

func addedDiff(ctx context.Context, dir, p string) (diff string, notShown bool, err error) {
	content, ns, rerr := ReadFile(ctx, dir, p)
	if rerr != nil {
		return "", false, nil
	}
	if ns {
		return "", true, nil
	}
	return synthAddedDiff(p, content), false, nil
}

func synthAddedDiff(p, content string) string {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", p, len(lines))
	for _, ln := range lines {
		b.WriteString("+")
		b.WriteString(ln)
		b.WriteString("\n")
	}
	return b.String()
}

// gitShowRev returns the content of a repo-relative path at a git rev. content is
// "" when the path is absent at that rev (or the rev/parent does not exist);
// oversize is true when the blob exceeds the size cap, so it is never read into
// memory. A context error (cancellation/timeout) is surfaced rather than masked as
// an absent path.
func gitShowRev(ctx context.Context, root, rev, path string) (content string, oversize bool, err error) {
	spec := rev + ":" + path
	sz := shell.NewCommandContext(ctx, "git", "-C", root, "cat-file", "-s", spec)
	if err := sz.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, nil // absent at rev
	}
	if n, perr := strconv.ParseInt(sz.StdOut().TrimSpace().String(), 10, 64); perr == nil && n > maxContentBytes {
		return "", true, nil
	}
	cmd := shell.NewCommandContext(ctx, "git", "-C", root, "show", spec)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, nil
	}
	return cmd.StdOut().String(), false, nil
}

// repoRelPath joins a client-supplied repo-relative path onto root, rejecting any
// path that escapes the repo (e.g. via "..").
func repoRelPath(root, path string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("gitstatus: path escapes repo: %s", path)
	}
	return full, nil
}

// repoRoot resolves the top-level directory of the repo containing dir so every
// git command and working-file read uses consistent repo-root-relative paths.
func repoRoot(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", errors.New("gitstatus: empty working directory")
	}
	cmd := shell.NewCommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gitstatus: not a git repository: %s", dir)
	}
	return cmd.StdOut().TrimSpace().String(), nil
}

// isBinary uses git's own heuristic: a NUL byte means treat as binary.
func isBinary(s string) bool {
	return strings.IndexByte(s, 0) >= 0
}

// isHexSHA reports whether s is a plausible git object name: 4–64 hex digits.
// Client-supplied revs are checked against this before reaching git so they can
// never be parsed as options (e.g. "--output=FILE").
func isHexSHA(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Commit is one entry in the branch/unpushed commit log.
type Commit struct {
	SHA     string
	Short   string
	Subject string
	Author  string
	UnixSec int64 // authored time
}

// CommitLog is the commit list plus whether its scope is unpushed-vs-remote.
type CommitLog struct {
	Commits  []Commit
	Unpushed bool
}

// Commits lists commits reachable from HEAD but not from the base ref, newest
// first, excluding merges. The base prefers the remote default branch (so the list
// is "work not yet on the remote"), falling back to a local main/master. Unpushed
// is true when a remote base ref was used. An empty range yields no commits, no
// error.
func Commits(ctx context.Context, dir string) (CommitLog, error) {
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return CommitLog{}, err
	}
	base, unpushed := baseRef(ctx, root)
	if base == "" {
		return CommitLog{}, nil
	}
	mb := mergeBase(ctx, root, base)
	if mb == "" {
		return CommitLog{}, nil
	}
	commits, err := commitLog(ctx, root, mb)
	if err != nil {
		return CommitLog{}, err
	}
	return CommitLog{Commits: commits, Unpushed: unpushed}, nil
}

// CommitsSince lists commits reachable from HEAD but not from base, newest
// first, excluding merges.
func CommitsSince(ctx context.Context, dir, base string) ([]Commit, error) {
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	return commitLog(ctx, root, base)
}

func commitLog(ctx context.Context, root, base string) ([]Commit, error) {
	const sep = "\x1f"
	format := strings.Join([]string{"%H", "%h", "%s", "%an", "%at"}, sep)
	cmd := shell.NewCommandContext(ctx, "git", "-C", root,
		"log", "--no-merges", "--format="+format, base+"..HEAD")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gitstatus: git log failed in %s: %w", root, err)
	}
	return parseCommitLog(cmd.StdOut().String(), sep), nil
}

func parseCommitLog(out, sep string) []Commit {
	var commits []Commit
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, sep)
		if len(f) != 5 {
			continue
		}
		unix, _ := strconv.ParseInt(f[4], 10, 64)
		commits = append(commits, Commit{
			SHA: f[0], Short: f[1], Subject: f[2], Author: f[3], UnixSec: unix,
		})
	}
	return commits
}

// baseRef resolves the ref to compare HEAD against and whether it is a remote ref
// (remote → the commit list represents unpushed work).
func baseRef(ctx context.Context, root string) (ref string, remote bool) {
	for _, r := range []string{"origin/HEAD", "origin/main", "origin/master"} {
		if revExists(ctx, root, r) {
			return r, true
		}
	}
	for _, r := range []string{"main", "master"} {
		if revExists(ctx, root, r) {
			return r, false
		}
	}
	return "", false
}

func revExists(ctx context.Context, root, ref string) bool {
	cmd := shell.NewCommandContext(ctx, "git", "-C", root,
		"rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return cmd.Run() == nil
}

func mergeBase(ctx context.Context, root, base string) string {
	cmd := shell.NewCommandContext(ctx, "git", "-C", root, "merge-base", base, "HEAD")
	if err := cmd.Run(); err != nil {
		return ""
	}
	return cmd.StdOut().TrimSpace().String()
}

// CommitFiles lists the files a commit changed relative to its first parent, or —
// for the root commit — everything it introduced.
func CommitFiles(ctx context.Context, dir, sha string) ([]ChangedFile, error) {
	if !isHexSHA(sha) {
		return nil, fmt.Errorf("gitstatus: invalid commit sha: %q", sha)
	}
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	// isHexSHA above guarantees sha is pure hex, so it can never be read as an
	// option (e.g. "--output=FILE") — no --end-of-options guard needed.
	cmd := shell.NewCommandContext(ctx, "git", "-C", root,
		"diff-tree", "--no-commit-id", "--name-status", "-r", "-M", "--root", "-z", sha)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gitstatus: git diff-tree failed: %w", err)
	}
	return parseNameStatusZ(cmd.StdOut().String()), nil
}

// CommitDiff returns the unified diff of a repo-relative path in one commit,
// against its first parent (or everything, for the root commit). origPath (a
// rename source, or "") lets git show the rename. notShown is true for a binary
// or oversized result.
func CommitDiff(ctx context.Context, dir, sha, p, origPath string) (diff string, notShown bool, err error) {
	if !isHexSHA(sha) {
		return "", false, fmt.Errorf("gitstatus: invalid commit sha: %q", sha)
	}
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return "", false, err
	}
	if _, err := repoRelPath(root, p); err != nil {
		return "", false, err
	}
	args := []string{"-C", root, "show", "--format=", "--no-show-signature", "--no-color", "-M", sha, "--", p}
	if origPath != "" {
		if _, err := repoRelPath(root, origPath); err != nil {
			return "", false, err
		}
		args = append(args, origPath)
	}
	cmd := shell.NewCommandContext(ctx, "git", args...)
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("gitstatus: git show failed: %w", err)
	}
	out := cmd.StdOut().String()
	if len(out) > maxContentBytes || isBinary(out) {
		return "", true, nil
	}
	return out, false, nil
}

// parseNameStatusZ parses NUL-separated `git diff-tree --name-status -z` records.
// A record is "STATUS\0PATH"; a rename/copy (R/C) is "STATUS\0SRC\0DST".
func parseNameStatusZ(out string) []ChangedFile {
	tokens := strings.Split(out, "\x00")
	var files []ChangedFile
	for i := 0; i+1 < len(tokens); {
		status := tokens[i]
		if status == "" {
			i++
			continue
		}
		if c := status[0]; c == 'R' || c == 'C' {
			if i+2 >= len(tokens) {
				break
			}
			files = append(files, ChangedFile{
				Change:   ChangeRenamed,
				OrigPath: tokens[i+1],
				Path:     tokens[i+2],
			})
			i += 3
			continue
		}
		files = append(files, ChangedFile{
			Change: changeFromCode(status[0]),
			Path:   tokens[i+1],
		})
		i += 2
	}
	return files
}

func changeFromCode(c byte) ChangeType {
	switch c {
	case 'A':
		return ChangeAdded
	case 'D':
		return ChangeDeleted
	default:
		return ChangeModified
	}
}

// CommitFileContents returns a file's content before (parent) and after a commit,
// for rendering that commit's diff. old is "" for an added file or the root
// commit; new is "" for a deleted file; notShown marks binary or oversized files.
func CommitFileContents(ctx context.Context, dir, sha, path, origPath string) (old, new string, notShown bool, err error) {
	if !isHexSHA(sha) {
		return "", "", false, fmt.Errorf("gitstatus: invalid commit sha: %q", sha)
	}
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return "", "", false, err
	}
	oldPath := path
	if origPath != "" {
		oldPath = origPath
	}
	var oversize bool
	if old, oversize, err = gitShowRev(ctx, root, sha+"^", oldPath); err != nil {
		return "", "", false, err
	} else if oversize {
		return "", "", true, nil
	}
	if new, oversize, err = gitShowRev(ctx, root, sha, path); err != nil {
		return "", "", false, err
	} else if oversize {
		return "", "", true, nil
	}
	if isBinary(old) || isBinary(new) {
		return "", "", true, nil
	}
	return old, new, false, nil
}
