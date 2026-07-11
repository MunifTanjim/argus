package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/session"
)

type exportDoneMsg struct {
	path string
	err  error
}

// actExportSession exports the transcript currently open (live or history) to a
// .argus file in the working directory, reporting the path via a flash message.
func (m model) actExportSession(tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.viewer {
		return m, nil
	}
	agent, path, nodeID, md, ok := m.exportTarget()
	if !ok {
		m.flash = "export: no session open"
		return m, nil
	}
	client := m.client
	m.flash = "exporting…"
	return m, func() tea.Msg {
		var res api.ExportBundleResult
		err := client.Call(api.MethodSessionExport, api.ExportBundleParams{
			NodeID: nodeID, Agent: agent, TranscriptPath: path, Metadata: md,
		}, &res)
		if err != nil {
			return exportDoneMsg{err: err}
		}
		p, werr := writeExportFile(res.Filename, res.Data)
		return exportDoneMsg{path: p, err: werr}
	}
}

// exportTarget resolves the session to export. Export is History-only: either an
// open transcript or the cursor row in the session list.
func (m model) exportTarget() (agent, path, nodeID string, md bundle.Metadata, ok bool) {
	switch m.mode {
	case modeHistoryTranscript:
		if m.history.openPath == "" {
			return "", "", "", bundle.Metadata{}, false
		}
		md = historyExportMetadata(m.history.openSession, m.history.project)
		return m.history.openAgent, m.history.openPath, m.history.openNodeID, md, true
	case modeHistorySessions:
		if m.history.sessCursor >= len(m.history.sessions) {
			return "", "", "", bundle.Metadata{}, false
		}
		s := m.history.sessions[m.history.sessCursor]
		if s.TranscriptPath == "" {
			return "", "", "", bundle.Metadata{}, false
		}
		md = historyExportMetadata(s, m.history.project)
		return s.Agent, s.TranscriptPath, m.history.project.NodeID, md, true
	}
	return "", "", "", bundle.Metadata{}, false
}

func historyExportMetadata(s session.HistorySession, p session.HistoryProject) bundle.Metadata {
	return bundle.Metadata{
		Title:        s.Title,
		FirstMessage: s.FirstMessage,
		ModelName:    s.ModelName,
		ModelColor:   s.ModelColor,
		LastActivity: s.LastActivity,
		Tokens:       s.Tokens,
		TurnCount:    s.TurnCount,
		DurationMs:   s.DurationMs,
		Cwd:          p.Cwd,
		Repo:         p.Repo,
	}
}

// writeExportFile writes data to filename in the working directory, adding a
// numeric suffix if the name is taken. Returns the absolute path written.
func writeExportFile(filename string, data []byte) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// filename comes from the (possibly remote) node; strip any directory
	// components so it can only ever land in cwd, never traverse out of it.
	base := filepath.Base(filename)
	if base == "." || base == string(filepath.Separator) || base == ".." {
		return "", fmt.Errorf("export: node returned invalid filename %q", filename)
	}
	filename = base
	ext := filepath.Ext(filename)
	stem := filename[:len(filename)-len(ext)]
	target := nextAvailablePath(cwd, stem, ext)
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// nextAvailablePath returns dir/base+ext, adding a numeric "-N" suffix before
// ext until it finds a name that does not exist on disk.
func nextAvailablePath(dir, base, ext string) string {
	target := filepath.Join(dir, base+ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(target); err != nil {
			return target // free slot (not-exist) or unreadable; caller surfaces real errors
		}
		target = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
	}
}
