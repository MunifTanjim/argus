package antigravity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/histcache"
	"github.com/MunifTanjim/argus/internal/session"
)

func conversationsDir() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "conversations"), nil
}

// listConvIDs returns every conversation id that has a <id>.db.
func listConvIDs() ([]string, error) {
	dir, err := conversationsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".db") {
			ids = append(ids, strings.TrimSuffix(name, ".db"))
		}
	}
	return ids, nil
}

// cachedConvs returns a history item (+cwd) for every conversation, served from
// the disk cache keyed on the transcript file's mod time + size, so the sqlite
// lookups for cwd/model run only when a conversation changes. Both list views
// build on this.
func cachedConvs() ([]histcache.Entry, error) {
	ids, err := listConvIDs()
	if err != nil {
		return nil, err
	}
	live := make(map[string]struct{}, len(ids))
	out := make([]histcache.Entry, 0, len(ids))
	for _, id := range ids {
		tpath := transcriptPathFor(id)
		if tpath == "" {
			continue
		}
		fi, err := os.Stat(tpath)
		if err != nil {
			continue // no transcript on disk
		}
		live[id] = struct{}{}
		if e, ok := histcache.Get(Agent, id, fi.ModTime(), fi.Size()); ok {
			out = append(out, e)
			continue
		}
		name, color := conversationModel(id)
		e := histcache.Entry{
			Cwd: conversationWorkspace(id), // "" bucketed as unknown
			Session: session.HistorySession{
				SessionID:      id,
				TranscriptPath: tpath,
				ModelName:      name,
				ModelColor:     color,
				LastActivity:   fi.ModTime().UTC().Format(time.RFC3339),
			},
		}
		histcache.Put(Agent, id, fi.ModTime(), fi.Size(), e)
		out = append(out, e)
	}
	histcache.Prune(Agent, live)
	return out, nil
}

func listHistoryProjects() ([]session.HistoryProject, error) {
	entries, err := cachedConvs()
	if err != nil {
		return nil, err
	}
	type agg struct {
		count int
		last  string // RFC3339 UTC; lexicographic compare is chronological
	}
	byCwd := map[string]*agg{}
	for _, e := range entries {
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
	out := make([]session.HistoryProject, 0, len(byCwd))
	for cwd, a := range byCwd {
		repo := repoName(cwd)
		label := repo
		if label == "" {
			label = filepath.Base(cwd)
		}
		if cwd == "" {
			label = "(unknown)"
		}
		out = append(out, session.HistoryProject{
			ProjectDir:   cwd,
			Cwd:          cwd,
			Repo:         repo,
			Label:        label,
			SessionCount: a.count,
			LastActivity: a.last,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out, nil
}

func listHistorySessions(cwd string, limit, offset int) (session.HistorySessionPage, error) {
	entries, err := cachedConvs()
	if err != nil {
		return session.HistorySessionPage{}, err
	}
	var items []session.HistorySession
	for _, e := range entries {
		if e.Cwd != cwd {
			continue
		}
		items = append(items, e.Session)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].LastActivity > items[j].LastActivity })
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

func safeBrainPath(path string) (string, error) {
	root, err := brainDir()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", fmt.Errorf("transcript path outside antigravity brain root: %s", path)
	}
	return clean, nil
}
