package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

// claudeHome returns the ~/.claude root that a project transcript path lives
// under (…/.claude/projects/<proj>/<uuid>.jsonl → …/.claude).
func claudeHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".claude")
	}
	return ""
}

// collectSessionFiles gathers the session's files rooted under home (the
// ~/.claude dir), returning bundle-relative paths under "root/".
func collectSessionFiles(transcriptPath, home string) ([]adapter.BundledFile, error) {
	if home == "" {
		return nil, fmt.Errorf("collect: cannot determine ~/.claude home")
	}

	abs, err := filepath.Abs(transcriptPath)
	if err != nil {
		return nil, err
	}
	home, _ = filepath.Abs(home)

	// The entry must be a real session transcript under <home>/projects, not any
	// other readable file (e.g. credentials); everything else is derived from it.
	if !adapter.WithinDir(filepath.Join(home, "projects"), abs) || !strings.HasSuffix(abs, ".jsonl") {
		return nil, fmt.Errorf("collect: %s is not a session transcript", abs)
	}

	var out []adapter.BundledFile
	addRel := func(p string) {
		if bf, ok := adapter.RootedFile(home, p); ok {
			out = append(out, bf)
		}
	}
	add := func(p string) {
		// Lstat, not Stat: skip symlinks, whose target could sit outside home.
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			addRel(p)
		}
	}
	addTree := func(dir string) {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error { //nolint:errcheck
			if err == nil && d.Type().IsRegular() { // skip dirs and symlinks
				addRel(p)
			}
			return nil
		})
	}

	// Main transcript (required); must be a regular file (see add).
	if info, err := os.Lstat(abs); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("collect: %s is not a regular file", abs)
	}
	addRel(abs)

	// Subagents dir: <projects>/<proj>/<uuid>/subagents/*
	base := strings.TrimSuffix(filepath.Base(abs), ".jsonl")
	addTree(filepath.Join(filepath.Dir(abs), base, "subagents"))

	// Team member sessions: sibling top-level .jsonl created by team Task calls.
	if chunks, err := parser.ReadSession(abs); err == nil {
		if procs, err := parser.DiscoverTeamSessions(abs, chunks); err == nil {
			for _, p := range procs {
				if p.FilePath != "" {
					add(p.FilePath)
				}
			}
		}
	}

	// tasks/ and teams/ dirs, keyed by session-<shortid> (first UUID segment).
	short := sessionShort(base)
	addTree(filepath.Join(home, "tasks", "session-"+short))
	addTree(filepath.Join(home, "teams", "session-"+short))

	return out, nil
}
