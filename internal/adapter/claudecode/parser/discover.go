package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DiscoverProjectSessions scans a project directory's session .jsonl files
// (excluding agent_* subagent files) and returns them newest-first.
func DiscoverProjectSessions(projectDir string) ([]SessionInfo, error) {
	return discoverSessions(projectDir, func(path string, _ time.Time) sessionMetadata {
		return scanSessionMetadata(path)
	})
}

// ListAllProjectDirs returns every project directory under ~/.claude/projects.
// For cross-project lookup; single-project resolution should prefer CurrentProjectDir.
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

// SessionTitleRef is a lightweight session reference for name-based lookup;
// full metadata requires DiscoverProjectSessions.
type SessionTitleRef struct {
	Path      string
	SessionID string
	Title     string
	ModTime   time.Time
}

// scanSessionTitle returns a session's effective title (custom-title beats
// ai-title; last occurrence of each wins). Cheaper than scanSessionMetadata:
// lines over titleLineCap or lacking "title" are skipped before unmarshaling.
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

// discoverSessionTitles lists titled sessions in a project directory; untitled
// ones are omitted since they can't match a name lookup.
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

// FindTitleMatches finds titled sessions whose title matches query
// case-insensitively. Exact matches beat substring matches; newest-first
// within a tier. Reads only title metadata, not conversation content.
func FindTitleMatches(query string, projectDirs []string) ([]SessionTitleRef, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	var all []SessionTitleRef
	for _, d := range projectDirs {
		refs, err := discoverSessionTitles(d)
		if err != nil {
			continue // missing dir or permission error
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

// DiscoverAllProjectSessions merges DiscoverProjectSessions across directories,
// newest-first. Missing directories are silently skipped.
func DiscoverAllProjectSessions(projectDirs []string) ([]SessionInfo, error) {
	var all []SessionInfo
	for _, dir := range projectDirs {
		sessions, err := DiscoverProjectSessions(dir)
		if err != nil {
			continue // missing dir or permission error
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

// discoverSessions is the shared directory-walk for DiscoverProjectSessions and
// its cached variant; scan determines direct-scan vs cache-lookup.
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
		sessions = append(sessions, buildSessionInfo(path, info.ModTime(), scan(path, info.ModTime())))
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].ModTime.After(sessions[j].ModTime)
	})

	return sessions, nil
}

// ScanSessionInfo scans a single session file into a SessionInfo. modTime is the
// caller's already-known file mod time, so no extra stat is done.
func ScanSessionInfo(path string, modTime time.Time) SessionInfo {
	return buildSessionInfo(path, modTime, scanSessionMetadata(path))
}

// buildSessionInfo assembles a SessionInfo from a file's scanned metadata.
func buildSessionInfo(path string, modTime time.Time, meta sessionMetadata) SessionInfo {
	isOngoing := meta.isOngoing
	if isOngoing && time.Since(modTime) > OngoingStalenessThreshold {
		isOngoing = false
	}

	// Resolve title: custom (user rename) wins over AI-generated.
	title := meta.customTitle
	if title == "" {
		title = meta.aiTitle
	}

	return SessionInfo{
		Path:           path,
		SessionID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		ModTime:        modTime,
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
	}
}
