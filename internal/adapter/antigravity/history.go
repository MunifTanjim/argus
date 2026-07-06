package antigravity

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

type convMeta struct {
	id, cwd, transcriptPath, model, modelColor string
	lastActivity                               time.Time
}

func scanConvMeta(id string) (convMeta, bool) {
	tpath := transcriptPathFor(id)
	if tpath == "" {
		return convMeta{}, false
	}
	fi, err := os.Stat(tpath)
	if err != nil {
		return convMeta{}, false // no transcript on disk
	}
	cwd := conversationWorkspace(id) // "" bucketed as unknown
	name, color := conversationModel(id)
	return convMeta{
		id:             id,
		cwd:            cwd,
		transcriptPath: tpath,
		model:          name,
		modelColor:     color,
		lastActivity:   fi.ModTime(),
	}, true
}

func listHistoryProjects() ([]session.HistoryProject, error) {
	ids, err := listConvIDs()
	if err != nil {
		return nil, err
	}
	type agg struct {
		count int
		last  time.Time
	}
	byCwd := map[string]*agg{}
	for _, id := range ids {
		m, ok := scanConvMeta(id)
		if !ok {
			continue
		}
		a := byCwd[m.cwd]
		if a == nil {
			a = &agg{}
			byCwd[m.cwd] = a
		}
		a.count++
		if m.lastActivity.After(a.last) {
			a.last = m.lastActivity
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
			LastActivity: a.last.UTC().Format(time.RFC3339),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out, nil
}

func listHistorySessions(cwd string, limit, offset int) (session.HistorySessionPage, error) {
	ids, err := listConvIDs()
	if err != nil {
		return session.HistorySessionPage{}, err
	}
	var items []session.HistorySession
	for _, id := range ids {
		m, ok := scanConvMeta(id)
		if !ok || m.cwd != cwd {
			continue
		}
		items = append(items, session.HistorySession{
			SessionID:      m.id,
			TranscriptPath: m.transcriptPath,
			ModelName:      m.model,
			ModelColor:     m.modelColor,
			LastActivity:   m.lastActivity.UTC().Format(time.RFC3339),
		})
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
