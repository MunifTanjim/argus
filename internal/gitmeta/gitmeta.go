// Package gitmeta derives lightweight git metadata (the current branch) for a
// directory by reading git's on-disk files directly — no git subprocess.
package gitmeta

import (
	"os"
	"path/filepath"
	"strings"
)

// Branch returns the current branch of the git repository containing dir: the
// name after "ref: refs/heads/" in HEAD, or the short (7-char) commit SHA when
// HEAD is detached. Returns "" when dir is empty, not in a repo, or HEAD is
// unreadable.
func Branch(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(head))
	if ref, ok := strings.CutPrefix(s, "ref: refs/heads/"); ok {
		return ref
	}
	if strings.HasPrefix(s, "ref:") {
		// Symbolic ref outside refs/heads/ (e.g. a tag): not a branch.
		return ""
	}
	// Detached HEAD: HEAD holds a raw commit SHA.
	if len(s) >= 7 {
		return s[:7]
	}
	return ""
}

// findGitDir walks up from dir to the nearest ".git" entry and returns the git
// directory: the ".git" dir itself, or — for worktrees/submodules where ".git"
// is a file containing "gitdir: <path>" — the path it points to. Returns "" if
// none is found.
func findGitDir(dir string) string {
	for d := dir; d != ""; {
		git := filepath.Join(d, ".git")
		if info, err := os.Stat(git); err == nil {
			if info.IsDir() {
				return git
			}
			if target := readGitFile(git); target != "" {
				return target
			}
		}
		parent := filepath.Dir(d)
		if parent == d { // reached the filesystem root
			break
		}
		d = parent
	}
	return ""
}

// readGitFile reads a ".git" file's "gitdir: <path>" line and returns the path
// (absolute, resolved against the file's directory when relative). Returns "" on
// any problem.
func readGitFile(gitFile string) string {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(gitFile), target)
	}
	return target
}
