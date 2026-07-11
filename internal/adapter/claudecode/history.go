package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/histcache"
	"github.com/MunifTanjim/argus/internal/session"
)

// ListHistoryProjects returns every Claude Code project on disk, newest activity
// first. NodeID/NodeLabel are left for the gateway to stamp when aggregating
// across machines.
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
		key := p.Cwd
		if key == "" {
			key = p.Dir
		}
		out = append(out, session.HistoryProject{
			ProjectDir:   key,
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
// limit <= 0 returns all from offset on. Per-session metadata is served from the
// disk cache, keyed by each transcript's mod time + size.
func ListHistorySessions(cwd string, limit, offset int) (session.HistorySessionPage, error) {
	dir, err := resolveProjectDir(cwd)
	if err != nil {
		return session.HistorySessionPage{}, err
	}
	if dir == "" {
		return session.HistorySessionPage{}, nil // no matching Claude project
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return session.HistorySessionPage{}, err
	}
	var items []session.HistorySession
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "agent_") {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		if e, ok := histcache.Get(Agent, id, fi.ModTime(), fi.Size()); ok {
			items = append(items, e.Session)
			continue
		}
		in := parser.ScanSessionInfo(filepath.Join(dir, name), fi.ModTime())
		hs := toHistorySession(in)
		histcache.Put(Agent, id, fi.ModTime(), fi.Size(), histcache.Entry{Session: hs, Cwd: in.Cwd})
		items = append(items, hs)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastActivity > items[j].LastActivity })

	total := len(items)
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
	return session.HistorySessionPage{Items: items[offset:end], HasMore: end < total}, nil
}

// toHistorySession maps a scanned SessionInfo to the history list item, resolving
// the model's display name and color.
func toHistorySession(in parser.SessionInfo) session.HistorySession {
	return session.HistorySession{
		SessionID:      in.SessionID,
		Title:          in.Title,
		FirstMessage:   in.FirstMessage,
		TranscriptPath: in.Path,
		ModelName:      modelDisplayName(in.Model),
		ModelColor:     modelColorHex(in.Model),
		LastActivity:   in.ModTime.UTC().Format(time.RFC3339),
		Tokens:         in.ContextTokens,
		TurnCount:      in.TurnCount,
		DurationMs:     in.DurationMs,
	}
}

// ReadHistoryTranscript reads a past session's transcript by path, after
// confirming it lives under the Claude projects root.
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

// FindHistoryToolDetail returns one tool item's full body (by tool_use id) from a
// past session's transcript, after the projects-root path check.
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

func resolveProjectDir(key string) (string, error) {
	// An empty key matches no Claude project; Claude always keys by cwd.
	if key == "" {
		return "", nil
	}
	projects, err := parser.ListProjects()
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.Cwd == key || p.Dir == key {
			return p.Dir, nil
		}
	}
	return "", nil
}
