package parser

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ProjectName returns a display name for a project directory. Inside a git
// repo (incl. worktrees/submodules), uses the main repo root's name; otherwise
// falls back to filepath.Base(cwd) with any branch suffix trimmed.
func ProjectName(cwd, gitBranch string) string {
	if cwd == "" {
		return ""
	}
	cleaned := filepath.Clean(cwd)

	if root := findGitRepoRoot(cleaned); root != "" {
		return filepath.Base(root)
	}

	// No git repo: trim branch suffix from the dir name (handles offline
	// worktrees whose .git points to a non-existent main repo).
	name := filepath.Base(cleaned)
	name = trimBranchSuffix(name, gitBranch)
	return name
}

// findGitRepoRoot walks up from dir looking for .git, resolving .git files
// (worktrees/submodules) to the main repo root via commondir. Returns "" if none.
func findGitRepoRoot(dir string) string {
	if dir == "" {
		return ""
	}

	current := dir
	// If dir isn't a directory (e.g. a file path), start from its parent.
	if info, err := os.Stat(current); err == nil {
		if !info.IsDir() {
			current = filepath.Dir(current)
		}
	} else {
		// Path doesn't exist -- avoid walking non-paths.
		if !strings.ContainsRune(current, filepath.Separator) {
			return ""
		}
		current = filepath.Dir(current)
	}

	for {
		gitPath := filepath.Join(current, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				// Normal git repo -- this directory is the root.
				return current
			}
			if info.Mode().IsRegular() {
				// .git file -- worktree or submodule.
				if root := repoRootFromGitFile(current, gitPath); root != "" {
					return root
				}
				// Conservative fallback: treat the worktree directory as root.
				return current
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// repoRootFromGitFile resolves the main repo root from a .git file via its
// gitdir + commondir, falling back to parsing the worktrees path structure.
func repoRootFromGitFile(repoDir, gitFilePath string) string {
	gitDir := readGitDirFromFile(gitFilePath)
	if gitDir == "" {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Clean(filepath.Join(filepath.Dir(gitFilePath), gitDir))
	}

	commonDir := readCommonDir(gitDir)
	if commonDir != "" {
		if filepath.Base(commonDir) == ".git" {
			return filepath.Dir(commonDir)
		}
	}

	// Fallback: parse the worktrees path structure.
	// gitDir looks like /repo/.git/worktrees/<name>
	marker := string(filepath.Separator) + ".git" +
		string(filepath.Separator) + "worktrees" +
		string(filepath.Separator)
	if root, _, found := strings.Cut(gitDir, marker); found {
		if root != "" {
			return filepath.Clean(root)
		}
	}

	return repoDir
}

// readGitDirFromFile reads the "gitdir: <path>" reference from a .git file.
func readGitDirFromFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		const prefix = "gitdir:"
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// readCommonDir reads a git dir's commondir file (worktrees use it to point
// at the main repo's .git).
func readCommonDir(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(b))
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(gitDir, value))
}

// trimBranchSuffix strips a branch-name suffix from a dir named
// "project-branch-name". Default branches (main/master/trunk/develop/dev) are
// kept -- "project-main" is likely intentional.
func trimBranchSuffix(name, gitBranch string) string {
	branch := strings.TrimSpace(gitBranch)
	if name == "" || branch == "" {
		return name
	}
	branch = strings.TrimPrefix(branch, "refs/heads/")
	branchToken := normalizeBranchToken(branch)
	if branchToken == "" {
		return name
	}
	if isDefaultBranch(branchToken) {
		return name
	}

	for _, sep := range []string{"-", "_"} {
		suffix := sep + branchToken
		if strings.HasSuffix(strings.ToLower(name), strings.ToLower(suffix)) {
			base := strings.TrimRight(name[:len(name)-len(suffix)], "-_")
			if base != "" {
				return base
			}
		}
	}
	return name
}

// normalizeBranchToken lowercases a branch name and collapses any run of
// non-alphanumeric characters into a single dash.
func normalizeBranchToken(branch string) string {
	var b strings.Builder
	b.Grow(len(branch))

	lastDash := false
	for _, r := range branch {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		case r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsSpace(r):
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// isDefaultBranch reports common default branch names (not trimmed from dir names).
func isDefaultBranch(branch string) bool {
	switch strings.ToLower(strings.TrimSpace(branch)) {
	case "main", "master", "trunk", "develop", "dev":
		return true
	default:
		return false
	}
}

// ProjectDirForPath returns the ~/.claude/projects/<encoded> directory for an
// absolute path. Symlinks are resolved first so the encoding matches what
// Claude Code produces (e.g. macOS /tmp -> /private/tmp).
func ProjectDirForPath(absPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	encoded := encodePath(absPath)
	return filepath.Join(home, ".claude", "projects", encoded), nil
}

// encodePath encodes an absolute path into a Claude Code project dir name by
// replacing separators, dots, and underscores with "-". Lossy (literal dashes
// can't be reversed). Verified empirically against Claude Code's on-disk output.
func encodePath(absPath string) string {
	r := strings.NewReplacer(
		string(filepath.Separator), "-",
		".", "-",
		"_", "-",
	)
	return r.Replace(absPath)
}

// CurrentProjectDir returns the Claude projects directory for the CWD,
// resolving a worktree CWD to the main repo root (where sessions are stored).
func CurrentProjectDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	cwd = ResolveGitRoot(cwd)

	return ProjectDirForPath(cwd)
}

// ResolveGitRoot returns the git toplevel for dir (resolving worktrees to the
// main working tree root), or dir itself if it's not a git repo.
func ResolveGitRoot(dir string) string {
	if root := findGitRepoRoot(dir); root != "" {
		return root
	}
	return dir
}
