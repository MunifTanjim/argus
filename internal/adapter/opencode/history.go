package opencode

import "github.com/MunifTanjim/argus/internal/session"

func listHistoryProjects() ([]session.HistoryProject, error) { return nil, nil }

func listHistorySessions(string, int, int) (session.HistorySessionPage, error) {
	return session.HistorySessionPage{}, nil
}
