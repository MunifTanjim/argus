package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DiscoverProjectSessions finds all session .jsonl files in a project directory,
// scans each for metadata, and returns them sorted by modification time (newest first).
// Subagent files (agent_*) are excluded.
func DiscoverProjectSessions(projectDir string) ([]SessionInfo, error) {
	return discoverSessions(projectDir, func(path string, _ time.Time) sessionMetadata {
		return scanSessionMetadata(path)
	})
}

// ListAllProjectDirs returns every Claude Code project directory under
// ~/.claude/projects. Used for name-based session lookup that spans projects;
// name resolution inside a single project should prefer CurrentProjectDir.
func ListAllProjectDirs() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(root, e.Name()))
	}
	return dirs, nil
}

// SessionTitleRef is a lightweight session reference for name-based lookup.
// It carries only the fields needed to open or display the session; full
// metadata requires DiscoverProjectSessions.
type SessionTitleRef struct {
	Path      string
	SessionID string
	Title     string
	ModTime   time.Time
}

// scanSessionTitle reads a session file and returns its effective title
// (custom-title wins over ai-title; last occurrence of each wins). It
// avoids the full scanSessionMetadata pipeline — no preview extraction,
// no ongoing detection, no turn counting, no JSON parsing of content
// lines. Lines over titleLineCap bytes or lacking the "title" substring
// are rejected before unmarshaling, so content-bearing entries cost only
// a length check and a byte scan.
func scanSessionTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	const titleLineCap = 512 // title entries are tiny; real content is KB+

	lr := newLineReader(f)
	var custom, ai string
	for {
		line, ok := lr.next()
		if !ok {
			break
		}
		if len(line) > titleLineCap {
			continue
		}
		if !strings.Contains(line, `"custom-title"`) && !strings.Contains(line, `"ai-title"`) {
			continue
		}
		var raw struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		switch raw.Type {
		case "custom-title":
			if raw.CustomTitle != "" {
				custom = raw.CustomTitle
			}
		case "ai-title":
			if raw.AITitle != "" {
				ai = raw.AITitle
			}
		}
	}
	if custom != "" {
		return custom
	}
	return ai
}

// discoverSessionTitles lists every titled session in a project directory.
// Untitled sessions are omitted — they can't match a name lookup. Much
// cheaper than DiscoverProjectSessions because it uses scanSessionTitle
// instead of scanSessionMetadata.
func discoverSessionTitles(projectDir string) ([]SessionTitleRef, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}
	var refs []SessionTitleRef
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if strings.HasPrefix(name, "agent_") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(projectDir, name)
		title := scanSessionTitle(path)
		if title == "" {
			continue
		}
		refs = append(refs, SessionTitleRef{
			Path:      path,
			SessionID: strings.TrimSuffix(name, ".jsonl"),
			Title:     title,
			ModTime:   info.ModTime(),
		})
	}
	return refs, nil
}

// FindTitleMatches searches the given project directories for titled sessions
// whose Title (custom-title or ai-title) matches the query case-insensitively.
// Exact matches win over substring matches; within a tier, newest-first order.
//
// This function reads only the title metadata from each session — it does not
// scan conversation content — so cost scales with the number of session files,
// not their total size.
func FindTitleMatches(query string, projectDirs []string) ([]SessionTitleRef, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	var all []SessionTitleRef
	for _, d := range projectDirs {
		refs, err := discoverSessionTitles(d)
		if err != nil {
			continue // missing dir or permission error — skip
		}
		all = append(all, refs...)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].ModTime.After(all[j].ModTime)
	})
	lower := strings.ToLower(query)
	var exact, partial []SessionTitleRef
	for _, r := range all {
		t := strings.ToLower(r.Title)
		switch {
		case t == lower:
			exact = append(exact, r)
		case strings.Contains(t, lower):
			partial = append(partial, r)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return partial, nil
}

// DiscoverAllProjectSessions finds sessions across multiple project directories
// (main + worktree dirs). Calls DiscoverProjectSessions on each, merges results,
// and sorts by ModTime descending. Missing directories are silently skipped.
func DiscoverAllProjectSessions(projectDirs []string) ([]SessionInfo, error) {
	var all []SessionInfo
	for _, dir := range projectDirs {
		sessions, err := DiscoverProjectSessions(dir)
		if err != nil {
			continue // missing dir or permission error -- skip
		}
		all = append(all, sessions...)
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].ModTime.After(all[j].ModTime)
	})

	return all, nil
}

// scanFn returns session metadata for a given file path and modTime.
type scanFn func(path string, modTime time.Time) sessionMetadata

// discoverSessions is the shared directory-walk logic for DiscoverProjectSessions
// and its cached variant. The scan function determines how metadata is obtained
// (direct scan vs cache lookup).
func discoverSessions(projectDir string, scan scanFn) ([]SessionInfo, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}

	var sessions []SessionInfo
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if strings.HasPrefix(name, "agent_") {
			continue
		}

		info, err := de.Info()
		if err != nil {
			continue
		}

		path := filepath.Join(projectDir, name)
		meta := scan(path, info.ModTime())

		// Skip ghost sessions (e.g. only file-history-snapshot entries).
		if meta.turnCount == 0 {
			continue
		}

		isOngoing := meta.isOngoing
		if isOngoing && time.Since(info.ModTime()) > OngoingStalenessThreshold {
			isOngoing = false
		}

		// Resolve title: custom (user rename) wins over AI-generated.
		title := meta.customTitle
		if title == "" {
			title = meta.aiTitle
		}

		sessions = append(sessions, SessionInfo{
			Path:           path,
			SessionID:      strings.TrimSuffix(name, ".jsonl"),
			ModTime:        info.ModTime(),
			Title:          title,
			FirstMessage:   meta.firstMsg,
			LastPrompt:     meta.lastPrompt,
			TurnCount:      meta.turnCount,
			IsOngoing:      isOngoing,
			ContextTokens:  meta.contextTokens,
			DurationMs:     meta.durationMs,
			Model:          meta.model,
			Cwd:            meta.cwd,
			GitBranch:      meta.gitBranch,
			PermissionMode: meta.permissionMode,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}
