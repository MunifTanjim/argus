package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectInfo is a lightweight aggregate of one Claude Code project directory:
// counts/times come from a directory stat, Cwd from a bounded scan of the
// newest transcript -- no full transcript reads.
type ProjectInfo struct {
	Dir          string    // absolute ~/.claude/projects/<encoded> path
	Cwd          string    // working directory recorded in the newest transcript
	SessionCount int       // number of (non-subagent) .jsonl transcripts
	LastActivity time.Time // newest transcript mod time
}

// ListProjects returns every Claude Code project with session count, last
// activity, and cwd, sorted newest-first. Cheap: only the newest transcript
// per project is opened. Projects with no transcripts are omitted.
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

// scanSessionCwd reads a transcript until the first non-empty cwd, bounded to
// maxLines so the scan stays cheap on huge transcripts.
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
