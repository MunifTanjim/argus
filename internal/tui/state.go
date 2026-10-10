package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/MunifTanjim/argus/internal/atomicfile"
)

// tuiState is the TUI's UI state kept across runs in tui.json.
type tuiState struct {
	Sessions sessionsState `json:"sessions"`
}

type sessionsState struct {
	GroupBy string `json:"group_by,omitempty"`
}

// loadGroupBy reads the saved home list grouping. It always returns a usable
// dimension (host by default); the error says why the file was not used.
func loadGroupBy(path string) (groupBy, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return groupByHost, nil
	}
	if err != nil {
		return groupByHost, err
	}
	var st tuiState
	if err := json.Unmarshal(data, &st); err != nil {
		return groupByHost, fmt.Errorf("%s: %w", path, err)
	}
	if st.Sessions.GroupBy == "" {
		return groupByHost, nil
	}
	g, ok := parseGroupBy(st.Sessions.GroupBy)
	if !ok {
		return groupByHost, fmt.Errorf("%s: unknown sessions.group_by %q", path, st.Sessions.GroupBy)
	}
	return g, nil
}

// saveGroupBy writes sessions.group_by to path and keeps every other key. A file
// that is not a JSON object is replaced.
func saveGroupBy(path string, g groupBy) error {
	var doc map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		_ = json.Unmarshal(data, &doc)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	sessions, _ := doc["sessions"].(map[string]any)
	if sessions == nil {
		sessions = map[string]any{}
	}
	sessions["group_by"] = string(g)
	doc["sessions"] = sessions
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(out, '\n'))
}
