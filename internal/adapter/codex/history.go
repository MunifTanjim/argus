package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

func sessionsRoot() (string, error) {
	home, err := codexHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

func rolloutFiles() ([]string, error) {
	root, err := sessionsRoot()
	if err != nil {
		return nil, err
	}
	return filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*.jsonl"))
}

type rolloutMeta struct {
	id, cwd, model, firstMessage string
	tokens, turns                int
	lastActivity                 time.Time
}

// ok=false when the file is empty/unreadable or records no cwd.
func scanMeta(path string) (rolloutMeta, bool) {
	lines, err := scanRollout(path)
	if err != nil || len(lines) == 0 {
		return rolloutMeta{}, false
	}
	var m rolloutMeta
	for _, ln := range lines {
		switch ln.Type {
		case "session_meta":
			if ln.Payload.Cwd != "" {
				m.cwd = ln.Payload.Cwd
			}
			if ln.Payload.ID != "" {
				m.id = ln.Payload.ID
			}
		case "turn_context":
			if ln.Payload.Model != "" {
				m.model = ln.Payload.Model
			}
		case "event_msg":
			if ln.Payload.Info != nil && ln.Payload.Info.Total.TotalTokens > 0 {
				m.tokens = ln.Payload.Info.Total.TotalTokens
			}
		case "response_item":
			if ln.Payload.Role == "user" {
				m.turns++
				if m.firstMessage == "" {
					text := firstLine(strings.TrimSpace(contentText(ln.Payload.Content)))
					m.firstMessage = text
				}
			}
		}
	}
	if m.cwd == "" {
		return rolloutMeta{}, false
	}
	if fi, err := os.Stat(path); err == nil {
		m.lastActivity = fi.ModTime()
	}
	if m.id == "" {
		m.id = threadIDFromName(filepath.Base(path))
	}
	return m, true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Format: rollout-<ts>-<id>.jsonl
func threadIDFromName(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if i := strings.LastIndex(name, "-"); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

func safeSessionsPath(path string) (string, error) {
	root, err := sessionsRoot()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(path)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return "", fmt.Errorf("transcript path outside codex sessions root: %s", path)
	}
	return clean, nil
}

func listHistoryProjects() ([]session.HistoryProject, error) {
	files, err := rolloutFiles()
	if err != nil {
		return nil, err
	}
	type agg struct {
		count int
		last  time.Time
	}
	byCwd := map[string]*agg{}
	for _, f := range files {
		m, ok := scanMeta(f)
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

// Agent is left unset; the node layer stamps it.
func listHistorySessions(cwd string, limit, offset int) (session.HistorySessionPage, error) {
	files, err := rolloutFiles()
	if err != nil {
		return session.HistorySessionPage{}, err
	}
	modelNames := loadModelNames()
	var items []session.HistorySession
	for _, f := range files {
		m, ok := scanMeta(f)
		if !ok || m.cwd != cwd {
			continue
		}
		items = append(items, session.HistorySession{
			SessionID:      m.id,
			FirstMessage:   m.firstMessage,
			TranscriptPath: f,
			ModelName:      displayModel(m.model, modelNames),
			ModelColor:     modelColorFor(m.model),
			LastActivity:   m.lastActivity.UTC().Format(time.RFC3339),
			Tokens:         m.tokens,
			TurnCount:      m.turns,
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
