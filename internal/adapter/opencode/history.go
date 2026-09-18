package opencode

import (
	"context"
	"path/filepath"
	"sort"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

var historyDial func() (*client, bool)

func dial() (*client, bool) {
	if historyDial != nil {
		return historyDial()
	}
	return serviceClient()
}

func listHistoryProjects() ([]session.HistoryProject, error) {
	c, ok := dial()
	if !ok {
		return nil, nil
	}
	sessions, err := c.listSessions(context.Background())
	if err != nil {
		return nil, err
	}

	type dirGroup struct {
		sessions []ocSession
		maxUpd   int64
	}
	groups := map[string]*dirGroup{}
	order := []string{}
	for _, s := range sessions {
		dir := s.Location.Directory
		g, exists := groups[dir]
		if !exists {
			g = &dirGroup{}
			groups[dir] = g
			order = append(order, dir)
		}
		g.sessions = append(g.sessions, s)
		if s.Time.Updated > g.maxUpd {
			g.maxUpd = s.Time.Updated
		}
	}

	projects := make([]session.HistoryProject, 0, len(groups))
	for _, dir := range order {
		g := groups[dir]
		lastActivity := time.UnixMilli(g.maxUpd).UTC().Format(time.RFC3339)
		label := filepath.Base(dir)
		projects = append(projects, session.HistoryProject{
			ProjectDir:   dir,
			Cwd:          dir,
			Label:        label,
			SessionCount: len(g.sessions),
			LastActivity: lastActivity,
		})
	}
	return projects, nil
}

func listHistorySessions(projectDir string, limit, offset int) (session.HistorySessionPage, error) {
	c, ok := dial()
	if !ok {
		return session.HistorySessionPage{}, nil
	}
	all, err := c.listSessions(context.Background())
	if err != nil {
		return session.HistorySessionPage{}, err
	}

	var filtered []ocSession
	for _, s := range all {
		if s.Location.Directory == projectDir {
			filtered = append(filtered, s)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Time.Updated > filtered[j].Time.Updated
	})

	total := len(filtered)
	if offset >= total {
		return session.HistorySessionPage{Items: []session.HistorySession{}, HasMore: false}, nil
	}
	end := offset + limit
	hasMore := end < total
	if end > total {
		end = total
	}
	page := filtered[offset:end]

	items := make([]session.HistorySession, len(page))
	for i, s := range page {
		items[i] = session.HistorySession{
			SessionID:      s.ID,
			Agent:          Agent,
			Resumable:      true,
			Title:          s.Title,
			TranscriptPath: s.ID,
			LastActivity:   time.UnixMilli(s.Time.Updated).UTC().Format(time.RFC3339),
		}
	}
	return session.HistorySessionPage{Items: items, HasMore: hasMore}, nil
}
