package node

import (
	"sort"

	"github.com/MunifTanjim/argus/internal/session"
)

// mergeProjects merges by ProjectDir: sum SessionCount, max LastActivity,
// first non-empty Repo/Label/Cwd. Newest first.
func mergeProjects(lists [][]session.HistoryProject) []session.HistoryProject {
	byKey := map[string]*session.HistoryProject{}
	var order []string
	for _, list := range lists {
		for _, p := range list {
			cur, ok := byKey[p.ProjectDir]
			if !ok {
				cp := p
				byKey[p.ProjectDir] = &cp
				order = append(order, p.ProjectDir)
				continue
			}
			cur.SessionCount += p.SessionCount
			if p.LastActivity > cur.LastActivity {
				cur.LastActivity = p.LastActivity
			}
			if cur.Repo == "" {
				cur.Repo = p.Repo
			}
			if cur.Label == "" {
				cur.Label = p.Label
			}
			if cur.Cwd == "" {
				cur.Cwd = p.Cwd
			}
		}
	}
	out := make([]session.HistoryProject, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out
}

func mergeSessions(items []session.HistorySession, offset, limit int) session.HistorySessionPage {
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
	return session.HistorySessionPage{Items: items[offset:end], HasMore: end < total}
}
