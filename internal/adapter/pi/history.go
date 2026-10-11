package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

// histSession is one session file's list-view metadata, gathered in a single
// streaming pass (no full fold).
type histSession struct {
	id     string
	cwd    string
	name   string
	title  string // first user message, first line
	model  string // last assistant message's model
	tokens int
	mtime  time.Time
	path   string
}

// scanSessions walks the sessions root for *.jsonl files. rootOverride scopes
// tests to a temp dir.
func scanSessions(rootOverride string) []histSession {
	root := rootOverride
	if root == "" {
		var err error
		root, err = sessionsRoot()
		if err != nil {
			return nil
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, "--*--", "*.jsonl"))
	if err != nil {
		return nil
	}
	var out []histSession
	for _, p := range matches {
		if hs, ok := scanOne(p); ok {
			out = append(out, hs)
		}
	}
	return out
}

func scanOne(path string) (histSession, bool) {
	f, err := os.Open(path)
	if err != nil {
		return histSession{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return histSession{}, false
	}
	hs := histSession{path: path, mtime: info.ModTime()}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		switch probe.Type {
		case "session":
			var h sessionHeader
			if json.Unmarshal(line, &h) == nil {
				hs.id = h.ID
				hs.cwd = h.Cwd
				hs.name = h.Name
			}
		case "session_info":
			var e sessionEntry
			if json.Unmarshal(line, &e) == nil && e.Name != "" {
				hs.name = e.Name
			}
		case "message":
			var e sessionEntry
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			var m agentMessage
			if json.Unmarshal(e.Message, &m) != nil {
				continue
			}
			switch m.Role {
			case "user":
				if hs.title == "" {
					hs.title = firstLine(messageContentText(m.Content))
				}
			case "assistant":
				if m.Model != "" {
					hs.model = m.Model
				}
				if m.Usage != nil {
					hs.tokens += m.Usage.Input + m.Usage.Output + m.Usage.Reasoning
				}
			}
		case "usage":
			var e sessionEntry
			if json.Unmarshal(line, &e) == nil && e.Usage != nil {
				hs.tokens += e.Usage.Input + e.Usage.Output + e.Usage.Reasoning
			}
		}
	}
	if hs.id == "" {
		// Fall back to the filename's <ts>_<id>.jsonl suffix.
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if i := strings.LastIndexByte(base, '_'); i >= 0 && i+1 < len(base) {
			hs.id = base[i+1:]
		}
	}
	if hs.id == "" {
		return histSession{}, false
	}
	return hs, true
}

func listHistoryProjects() ([]session.HistoryProject, error) {
	return listHistoryProjectsIn("")
}

func listHistoryProjectsIn(root string) ([]session.HistoryProject, error) {
	type group struct {
		n      int
		maxMod time.Time
	}
	groups := map[string]*group{}
	var order []string
	for _, hs := range scanSessions(root) {
		dir := hs.cwd
		if dir == "" {
			continue
		}
		g, ok := groups[dir]
		if !ok {
			g = &group{}
			groups[dir] = g
			order = append(order, dir)
		}
		g.n++
		if hs.mtime.After(g.maxMod) {
			g.maxMod = hs.mtime
		}
	}
	projects := make([]session.HistoryProject, 0, len(groups))
	for _, dir := range order {
		g := groups[dir]
		projects = append(projects, session.HistoryProject{
			ProjectDir:   dir,
			Cwd:          dir,
			Label:        filepath.Base(dir),
			SessionCount: g.n,
			LastActivity: g.maxMod.UTC().Format(time.RFC3339),
		})
	}
	return projects, nil
}

func listHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return listHistorySessionsIn("", projectDir, limit, offset)
}

func listHistorySessionsIn(root, projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	var filtered []histSession
	for _, hs := range scanSessions(root) {
		if hs.cwd == projectDir {
			filtered = append(filtered, hs)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].mtime.After(filtered[j].mtime) })
	total := len(filtered)
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
	items := make([]session.HistorySession, 0, end-offset)
	for _, hs := range filtered[offset:end] {
		title := hs.name
		if title == "" {
			title = hs.title
		}
		items = append(items, session.HistorySession{
			SessionID:      hs.id,
			Agent:          Agent,
			Resumable:      true,
			Title:          title,
			ModelName:      hs.model,
			Tokens:         hs.tokens,
			TranscriptPath: hs.path,
			LastActivity:   hs.mtime.UTC().Format(time.RFC3339),
		})
	}
	return session.HistorySessionPage{Items: items, HasMore: end < total}, nil
}
