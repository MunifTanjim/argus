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
	Sessions    sessionsState    `json:"sessions"`
	LeftSidebar leftSidebarSaved `json:"left_sidebar"`
}

type sessionsState struct {
	GroupBy string `json:"group_by,omitempty"`
}

type leftSidebarSaved struct {
	Tab string `json:"tab,omitempty"`
}

// readState reads tui.json; a missing file is an empty state.
func readState(path string) (tuiState, error) {
	var st tuiState
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return tuiState{}, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// loadGroupBy reads the saved home list grouping. It always returns a usable
// dimension (host by default); the error says why the file was not used.
func loadGroupBy(path string) (groupBy, error) {
	st, err := readState(path)
	if err != nil || st.Sessions.GroupBy == "" {
		return groupByHost, err
	}
	g, ok := parseGroupBy(st.Sessions.GroupBy)
	if !ok {
		return groupByHost, fmt.Errorf("%s: unknown sessions.group_by %q", path, st.Sessions.GroupBy)
	}
	return g, nil
}

// loadLeftTab reads the saved left sidebar tab. It always returns a usable tab
// (Projects by default); the error says why the file was not used.
func loadLeftTab(path string) (leftTab, error) {
	st, err := readState(path)
	if err != nil || st.LeftSidebar.Tab == "" {
		return leftProjects, err
	}
	t, ok := parseLeftTab(st.LeftSidebar.Tab)
	if !ok {
		return leftProjects, fmt.Errorf("%s: unknown left_sidebar.tab %q", path, st.LeftSidebar.Tab)
	}
	return t, nil
}

func saveGroupBy(path string, g groupBy) error {
	return saveState(path, "sessions", "group_by", string(g))
}

func saveLeftTab(path string, t leftTab) error {
	return saveState(path, "left_sidebar", "tab", t.key())
}

// saveState writes section.key to path and keeps every other key. A file that
// is not a JSON object is replaced.
func saveState(path, section, key, value string) error {
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
	sec, _ := doc[section].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
	}
	sec[key] = value
	doc[section] = sec
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(out, '\n'))
}
