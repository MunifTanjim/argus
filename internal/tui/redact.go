package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/bundle"
)

// redactPreparedMsg carries a staged redaction: the finished bundle in a temp
// file, plus the report whose warnings gate the save.
type redactPreparedMsg struct {
	tempPath string // staged bundle awaiting confirmation
	outPath  string // rename target on confirm
	report   bundle.Report
	err      error
}

type redactDoneMsg struct {
	path     string
	warnings []string // content that could not be scrubbed and remains in the export
	sidecar  string   // path of the written .warnings.txt, if any
	err      error
}

// redactPrepareCmd redacts into a temp tree and stages the bundle as a hidden
// sibling of the destination; redactCommitCmd creates the final -redacted name
// only on confirm, so an unacknowledged leak never lands under a clean name.
func (m model) redactPrepareCmd() tea.Cmd {
	srcDir, out := m.redactSrcDir, redactOutputPath(m.bundlePath)
	lits := append([]string(nil), m.redact.literals...)
	return func() tea.Msg {
		tmpDir, err := os.MkdirTemp("", "argus-redact-*")
		if err != nil {
			return redactPreparedMsg{err: err}
		}
		defer os.RemoveAll(tmpDir)
		rep, err := bundle.RedactTree(srcDir, tmpDir, lits)
		if err != nil {
			return redactPreparedMsg{err: err}
		}
		// Same dir as the destination so commit is an atomic rename.
		f, err := os.CreateTemp(filepath.Dir(out), ".argus-redacted-*")
		if err != nil {
			return redactPreparedMsg{err: err}
		}
		tmpPath := f.Name()
		if err := bundle.WriteDir(f, tmpDir); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return redactPreparedMsg{err: err}
		}
		if err := f.Close(); err != nil {
			os.Remove(tmpPath)
			return redactPreparedMsg{err: err}
		}
		return redactPreparedMsg{tempPath: tmpPath, outPath: out, report: rep}
	}
}

// redactCommitCmd renames the staged bundle to its final name and, when content
// couldn't be scrubbed, writes a sidecar listing the files that still hold secrets.
func (m model) redactCommitCmd() tea.Cmd {
	tmpPath, out := m.redact.tempPath, m.redact.outPath
	warnings := append([]string(nil), m.redact.report.Warnings...)
	return func() tea.Msg {
		if err := os.Rename(tmpPath, out); err != nil {
			os.Remove(tmpPath)
			return redactDoneMsg{err: err}
		}
		sidecar := ""
		if len(warnings) > 0 {
			if err := writeWarningsSidecar(out, warnings); err == nil {
				sidecar = warningsSidecarPath(out)
			}
		}
		return redactDoneMsg{path: out, warnings: warnings, sidecar: sidecar}
	}
}

// redactAbortCmd deletes a staged bundle the user declined to save.
func redactAbortCmd(tmpPath string) tea.Cmd {
	return func() tea.Msg {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
		return nil
	}
}

// warningsSidecarPath is the sidecar path for a bundle's unscrubbed-content notes.
func warningsSidecarPath(bundlePath string) string {
	return bundlePath + ".warnings.txt"
}

func writeWarningsSidecar(bundlePath string, warnings []string) error {
	body := "Secrets that could NOT be removed from " + filepath.Base(bundlePath) +
		" — review before sharing:\n\n" + strings.Join(warnings, "\n") + "\n"
	return os.WriteFile(warningsSidecarPath(bundlePath), []byte(body), 0o644)
}

// redactOutputPath returns <stem>-redacted<ext> next to srcPath, suffixed if taken.
func redactOutputPath(srcPath string) string {
	dir := filepath.Dir(srcPath)
	ext := filepath.Ext(srcPath)
	stem := filepath.Base(srcPath)
	stem = stem[:len(stem)-len(ext)]
	return nextAvailablePath(dir, stem+"-redacted", ext)
}

// redactListActive reports whether the queued-redactions list is open.
func (m model) redactListActive() bool {
	return m.redactActive() && m.redact.listActive
}

// redactListBody renders the queued redactions with the delete cursor. Literals
// are shown in full so they can be told apart and managed.
func (m model) redactListBody() string {
	if len(m.redact.literals) == 0 {
		return dimStyle.Render("no redactions queued — press d to add a secret")
	}
	var b strings.Builder
	b.WriteString(asstStyle.Render(fmt.Sprintf("queued redactions (%d)", len(m.redact.literals))))
	b.WriteString("\n\n")
	for i, lit := range m.redact.literals {
		marker := "  "
		row := dimStyle.Render(lit)
		if i == m.redact.listCursor {
			marker = cursorStyle.Render("▸ ")
			row = cursorStyle.Render(lit)
		}
		b.WriteString(marker + row)
		if i < len(m.redact.literals)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) redactFooter(base string) string {
	switch {
	case m.redact.inputActive:
		return asstStyle.Render("redact (paste secret): " + m.redact.input + "▊  enter add · esc cancel")
	case m.redact.listActive:
		return asstStyle.Render(fmt.Sprintf("redactions: %d  j/k move · x delete · esc close", len(m.redact.literals)))
	case m.redact.pendingSave && m.redact.warnConfirm && m.redact.report != nil:
		// Danger first, so it can't be truncated behind a y/n prompt.
		line := fmt.Sprintf("⚠ %d item(s) hold secrets that can't be removed — they WILL remain in the export. y save anyway · any cancel",
			len(m.redact.report.Warnings))
		return StyleErrorBold.Render(line)
	case m.redact.pendingSave && m.redact.report != nil:
		occ := 0
		for _, v := range m.redact.report.Counts {
			occ += v
		}
		line := fmt.Sprintf("redact %d secret(s), %d occurrence(s) → %s? y/n",
			len(m.redact.literals), occ, filepath.Base(m.redact.outPath))
		if zm := m.redact.report.ZeroMatch(m.redact.literals); len(zm) > 0 {
			line += "  ⚠ no match: " + strings.Join(zm, ", ")
		}
		return asstStyle.Render(line)
	case m.flash != "": // transient flash (save done / error) beats the queued-count hint
		return base
	case len(m.redact.literals) > 0:
		return asstStyle.Render(fmt.Sprintf("%d redaction(s) queued · d add · D list · W save", len(m.redact.literals)))
	case m.redactMode:
		return asstStyle.Render("redact: d add secret")
	}
	return base
}

// takePendingRedactSave consumes an armed save confirmation; non-"y" cancels. With
// warnConfirm, the first "y" only acknowledges the warning and a second saves, so a
// single reflexive keypress can't blow past "secrets will remain".
func (m model) takePendingRedactSave(msg tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if !m.redact.pendingSave {
		return m, nil, false
	}
	if msg.String() != "y" {
		tmp := m.redact.tempPath
		m.redact.pendingSave, m.redact.warnConfirm, m.redact.tempPath = false, false, ""
		return m, redactAbortCmd(tmp), true
	}
	if m.redact.warnConfirm {
		m.redact.warnConfirm = false // acknowledged; next y saves
		return m, nil, true
	}
	m.redact.pendingSave = false
	cmd := m.redactCommitCmd()
	m.redact.tempPath = ""
	return m, cmd, true
}

// redactActive reports whether redaction keys are live. Queued literals are
// bundle-wide, so redaction stays available in both the transcript and detail views.
func (m model) redactActive() bool {
	return m.redactMode && m.mode == modeHistoryTranscript
}

// handleRedactKey processes redaction keys and input. ok reports the key was consumed.
func (m model) handleRedactKey(msg tea.KeyPressMsg) (model, tea.Cmd, bool) {
	if !m.redactActive() {
		return m, nil, false
	}
	if m.redact.listActive {
		switch {
		case key.Matches(msg, transcriptKeys.Back):
			m.redact.listActive = false
			return m, nil, true
		case msg.Text == "j":
			m.redact.listCursor = min(m.redact.listCursor+1, max(0, len(m.redact.literals)-1))
			return m, nil, true
		case msg.Text == "k":
			m.redact.listCursor = max(0, m.redact.listCursor-1)
			return m, nil, true
		case msg.Text == "x":
			if i := m.redact.listCursor; i < len(m.redact.literals) {
				m.redact.literals = append(m.redact.literals[:i], m.redact.literals[i+1:]...)
				m.redact.listCursor = max(0, min(i, len(m.redact.literals)-1))
			}
			return m, nil, true
		case key.Matches(msg, transcriptKeys.Redact): // d: add another, then return here
			m.redact.listActive, m.redact.listReturn = false, true
			m.redact.inputActive, m.redact.input = true, ""
			return m, nil, true
		}
		return m, nil, true
	}
	if m.redact.inputActive {
		return m.handleRedactInput(msg)
	}
	switch {
	case key.Matches(msg, transcriptKeys.Redact):
		m.redact.inputActive = true
		m.redact.input = ""
		return m, nil, true
	case key.Matches(msg, transcriptKeys.RedactList):
		m.redact.listActive = true
		m.redact.listCursor = 0
		return m, nil, true
	case key.Matches(msg, transcriptKeys.RedactSave):
		if len(m.redact.literals) == 0 {
			m.flash = "no redactions queued"
			return m, nil, true
		}
		return m, m.redactPrepareCmd(), true
	}
	return m, nil, false
}

func (m model) handleRedactInput(msg tea.KeyPressMsg) (model, tea.Cmd, bool) {
	switch msg.Code {
	case tea.KeyEnter:
		if s := m.redact.input; s != "" {
			m.redact.literals = append(m.redact.literals, s)
			m.flash = "redaction queued"
		}
		m.redact.inputActive, m.redact.input = false, ""
		if m.redact.listReturn { // came from the list — reopen it on the new entry
			m.redact.listActive, m.redact.listReturn = true, false
			m.redact.listCursor = max(0, len(m.redact.literals)-1)
		}
		return m, nil, true
	case tea.KeyEscape:
		m.redact.inputActive, m.redact.input = false, ""
		if m.redact.listReturn {
			m.redact.listActive, m.redact.listReturn = true, false
		}
		return m, nil, true
	case tea.KeyBackspace:
		if n := len(m.redact.input); n > 0 {
			m.redact.input = m.redact.input[:n-1]
		}
		return m, nil, true
	}
	if msg.Text != "" {
		m.redact.input += msg.Text
	}
	return m, nil, true
}
