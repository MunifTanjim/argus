package pi

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/histcache"
	"github.com/MunifTanjim/argus/internal/session"
)

// sessionFiles lists the *.jsonl files under the sessions root. rootOverride
// scopes tests to a temp dir.
func sessionFiles(rootOverride string) []string {
	root := rootOverride
	if root == "" {
		var err error
		root, err = sessionsRoot()
		if err != nil {
			return nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(root, "--*--", "*.jsonl"))
	return matches
}

// cachedSessions returns a history entry for every session file, served from
// the disk cache and re-scanned only when a file changes.
func cachedSessions(rootOverride string) []histcache.Entry {
	files := sessionFiles(rootOverride)
	live := make(map[string]struct{}, len(files))
	out := make([]histcache.Entry, 0, len(files))
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		live[key] = struct{}{}
		if e, ok := histcache.Get(Agent, key, fi.ModTime(), fi.Size()); ok {
			e.Session.TranscriptPath = f // a moved file keeps its mtime and size
			out = append(out, e)
			continue
		}
		e, ok := scanOne(f, fi.ModTime())
		if !ok {
			continue
		}
		histcache.Put(Agent, key, fi.ModTime(), fi.Size(), e)
		out = append(out, e)
	}
	histcache.Prune(Agent, live)
	return out
}

// scanOne gathers a session file's list-view metadata in a single streaming
// pass (no full fold).
func scanOne(path string, mtime time.Time) (histcache.Entry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return histcache.Entry{}, false
	}
	defer f.Close()
	var id, cwd, name, title, model string
	var tokens int
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
				id, cwd, name = h.ID, h.Cwd, h.Name
			}
		case "session_info":
			var e sessionEntry
			if json.Unmarshal(line, &e) == nil && e.Name != "" {
				name = e.Name
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
				if title == "" {
					title = firstLine(messageContentText(m.Content))
				}
			case "assistant":
				if m.Model != "" {
					model = m.Model
				}
				tokens += m.Usage.tokens()
			case "toolResult":
				// Nested model work a tool did.
				tokens += m.Usage.tokens()
			}
		// pi counts the usage of summaries and of usage entries (cache warming,
		// say) in the session totals.
		case "usage", "compaction", "branch_summary":
			var e sessionEntry
			if json.Unmarshal(line, &e) == nil {
				tokens += e.Usage.tokens()
			}
		}
	}
	if id == "" {
		// Fall back to the filename's <ts>_<id>.jsonl suffix.
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if i := strings.LastIndexByte(base, '_'); i >= 0 && i+1 < len(base) {
			id = base[i+1:]
		}
	}
	if id == "" {
		return histcache.Entry{}, false
	}
	if name != "" {
		title = name
	}
	return histcache.Entry{
		Cwd: cwd,
		Session: session.HistorySession{
			SessionID:      id,
			Agent:          Agent,
			Resumable:      true,
			Title:          title,
			ModelName:      model,
			Tokens:         tokens,
			TranscriptPath: path,
			LastActivity:   mtime.UTC().Format(time.RFC3339),
		},
	}, true
}

func listHistoryProjects() ([]session.HistoryProject, error) {
	return listHistoryProjectsIn("")
}

func listHistoryProjectsIn(root string) ([]session.HistoryProject, error) {
	type agg struct {
		count int
		last  string // RFC3339 UTC; lexicographic compare is chronological
	}
	byCwd := map[string]*agg{}
	for _, e := range cachedSessions(root) {
		if e.Cwd == "" {
			continue
		}
		a := byCwd[e.Cwd]
		if a == nil {
			a = &agg{}
			byCwd[e.Cwd] = a
		}
		a.count++
		if e.Session.LastActivity > a.last {
			a.last = e.Session.LastActivity
		}
	}
	projects := make([]session.HistoryProject, 0, len(byCwd))
	for cwd, a := range byCwd {
		repo := gittree.RepoName(cwd)
		label := repo
		if label == "" {
			label = filepath.Base(cwd)
		}
		projects = append(projects, session.HistoryProject{
			ProjectDir:   cwd,
			Cwd:          cwd,
			Repo:         repo,
			Label:        label,
			SessionCount: a.count,
			LastActivity: a.last,
		})
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].LastActivity > projects[j].LastActivity })
	return projects, nil
}

func listHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	return listHistorySessionsIn("", projectDir, limit, offset)
}

func listHistorySessionsIn(root, projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	filtered := []session.HistorySession{}
	for _, e := range cachedSessions(root) {
		if e.Cwd == projectDir {
			filtered = append(filtered, e.Session)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].LastActivity > filtered[j].LastActivity })
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
	return session.HistorySessionPage{Items: filtered[offset:end], HasMore: end < total}, nil
}
