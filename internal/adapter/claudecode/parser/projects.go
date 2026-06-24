package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectInfo is a lightweight aggregate of one Claude Code project directory,
// built without reading every transcript: SessionCount and LastActivity come from
// a cheap directory stat, and Cwd from a bounded scan of the newest transcript.
type ProjectInfo struct {
	Dir          string    // absolute ~/.claude/projects/<encoded> path
	Cwd          string    // working directory recorded in the newest transcript
	SessionCount int       // number of (non-subagent) .jsonl transcripts
	LastActivity time.Time // newest transcript mod time
}

// ListProjects returns every Claude Code project directory with its session count,
// last-activity time, and cwd, sorted newest-first. It is cheap: only the newest
// transcript per project is opened (and only until its cwd is found). Projects with
// no session transcripts are omitted.
func ListProjects() ([]ProjectInfo, error) {
	dirs, err := ListAllProjectDirs()
	if err != nil {
		return nil, err
	}
	var out []ProjectInfo
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		count := 0
		var newest time.Time
		var newestPath string
		for _, de := range entries {
			if de.IsDir() {
				continue
			}
			name := de.Name()
			if !strings.HasSuffix(name, ".jsonl") || strings.HasPrefix(name, "agent_") {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue
			}
			count++
			if info.ModTime().After(newest) {
				newest = info.ModTime()
				newestPath = filepath.Join(dir, name)
			}
		}
		if count == 0 {
			continue
		}
		out = append(out, ProjectInfo{
			Dir:          dir,
			Cwd:          scanSessionCwd(newestPath),
			SessionCount: count,
			LastActivity: newest,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	return out, nil
}

// scanSessionCwd reads a transcript only until it finds the first non-empty cwd
// (which appears on early entries), bounded to a small number of lines so a project
// scan stays cheap even for huge transcripts.
func scanSessionCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	const maxLines = 100
	lr := newLineReader(f)
	for range maxLines {
		line, ok := lr.next()
		if !ok {
			break
		}
		if !strings.Contains(line, `"cwd"`) {
			continue
		}
		var raw struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal([]byte(line), &raw) == nil && raw.Cwd != "" {
			return raw.Cwd
		}
	}
	return ""
}
