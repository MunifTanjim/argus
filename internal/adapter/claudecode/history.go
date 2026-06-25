package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/session"
)

// ListHistoryProjects returns every Claude Code project discovered on disk, newest
// activity first, for the read-only History view. NodeID/NodeLabel are left empty
// (the gateway stamps them when aggregating across machines).
func ListHistoryProjects() ([]session.HistoryProject, error) {
	projects, err := parser.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]session.HistoryProject, 0, len(projects))
	for _, p := range projects {
		repo := repoName(p.Cwd)
		label := repo
		if label == "" {
			label = filepath.Base(p.Cwd)
		}
		if label == "" || label == "." || label == string(filepath.Separator) {
			label = filepath.Base(p.Dir)
		}
		out = append(out, session.HistoryProject{
			ProjectDir:   p.Dir,
			Cwd:          p.Cwd,
			Repo:         repo,
			Label:        label,
			SessionCount: p.SessionCount,
			LastActivity: p.LastActivity.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out, nil
}

// ListHistorySessions returns a newest-first window of a project's past sessions.
// limit <= 0 returns all from offset on. HasMore reports whether older sessions
// remain beyond the window.
func ListHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	infos, err := parser.DiscoverProjectSessions(projectDir) // already sorted newest-first
	if err != nil {
		return session.HistorySessionPage{}, err
	}
	total := len(infos)
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	window := infos[offset:end]
	items := make([]session.HistorySession, 0, len(window))
	for _, in := range window {
		items = append(items, session.HistorySession{
			SessionID:      in.SessionID,
			Title:          in.Title,
			FirstMessage:   in.FirstMessage,
			TranscriptPath: in.Path,
			Model:          in.Model,
			LastActivity:   in.ModTime.UTC().Format(time.RFC3339),
			Tokens:         in.ContextTokens,
			TurnCount:      in.TurnCount,
			DurationMs:     in.DurationMs,
		})
	}
	return session.HistorySessionPage{Items: items, HasMore: end < total}, nil
}

// ReadHistoryTranscript reads a past session's transcript by path, after confirming
// it lives under the Claude projects root (so a client can't read arbitrary files).
func ReadHistoryTranscript(path string) (TranscriptView, error) {
	clean, err := safeProjectsPath(path)
	if err != nil {
		return TranscriptView{}, err
	}
	return ReadTranscriptView(clean)
}

// ReadHistorySubagentView is the history counterpart of ReadSubagentView: it
// validates path is under the projects root, then folds the nested subagent.
func ReadHistorySubagentView(path, agentID string) (TranscriptView, bool, error) {
	clean, err := safeProjectsPath(path)
	if err != nil {
		return TranscriptView{}, false, err
	}
	return ReadSubagentView(clean, agentID)
}

// FindHistoryToolDetail returns one tool item's full body from a past session's
// transcript, addressed by tool_use id, after the same projects-root path check
// ReadHistoryTranscript applies.
func FindHistoryToolDetail(path, agentID, toolID string) (ToolDetail, bool, error) {
	clean, err := safeProjectsPath(path)
	if err != nil {
		return ToolDetail{}, false, err
	}
	return FindToolDetail(clean, agentID, toolID)
}

// safeProjectsPath cleans path and confirms it lives under the Claude projects
// root, so a client can't read arbitrary files.
func safeProjectsPath(path string) (string, error) {
	root, err := projectsRoot()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", fmt.Errorf("transcript path outside projects root: %s", path)
	}
	return clean, nil
}

// projectsRoot is ~/.claude/projects, matching the parser's project-dir assumption.
func projectsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}
