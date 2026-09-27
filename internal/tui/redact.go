package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
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
func (m tview) redactPrepareCmd() tea.Cmd {
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
func (m tview) redactCommitCmd() tea.Cmd {
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
func (m tview) redactListActive() bool {
	return m.redactActive() && m.redact.listActive
}

// redactListBody renders the queued redactions with the delete cursor. Literals
// are shown in full so they can be told apart and managed.
func (m tview) redactListBody() string {
	if len(m.redact.literals) == 0 {
		return dimStyle.Render("no redactions queued — press " + m.keyText(transcriptKeys.Redact) + " to add a secret")
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

func newRedactInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.EchoMode = textinput.EchoPassword
	ti.SetWidth(48)
	return ti
}

func (m tview) redactFooter(base string) string {
	switch {
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
	case len(m.keyBuf) > 0:
		return asstStyle.Render(m.keyHint())
	case m.redact.inputActive:
		return asstStyle.Render("redact (paste secret): " + m.redact.input.View() + "  enter add · esc cancel")
	case m.redact.listActive:
		return asstStyle.Render(fmt.Sprintf("redactions: %d  ", len(m.redact.literals)) + m.hintText(redactListKeys.Down,
			helpAs(redactListKeys.Remove, "delete"), helpAs(transcriptKeys.Back, "close")))
	case m.flash != "": // transient flash (save done / error) beats the queued-count hint
		return base
	case len(m.redact.literals) > 0:
		return asstStyle.Render(fmt.Sprintf("%d redaction(s) queued · ", len(m.redact.literals)) + m.hintText(
			helpAs(transcriptKeys.Redact, "add"), helpAs(transcriptKeys.RedactList, "list"),
			helpAs(transcriptKeys.RedactSave, "save")))
	case m.redactMode:
		return asstStyle.Render("redact: " + m.hintText(helpAs(transcriptKeys.Redact, "add secret")))
	}
	return base
}

// takePendingRedactSave consumes an armed save confirmation; non-"y" cancels. With
// warnConfirm, the first "y" only acknowledges the warning and a second saves, so a
// single reflexive keypress can't blow past "secrets will remain".
func (m tview) takePendingRedactSave(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.redact.pendingSave {
		return nil, false
	}
	if msg.String() != "y" {
		tmp := m.redact.tempPath
		m.redact.pendingSave, m.redact.warnConfirm, m.redact.tempPath = false, false, ""
		return redactAbortCmd(tmp), true
	}
	if m.redact.warnConfirm {
		m.redact.warnConfirm = false // acknowledged; next y saves
		return nil, true
	}
	m.redact.pendingSave = false
	cmd := m.redactCommitCmd()
	m.redact.tempPath = ""
	return cmd, true
}

// redactActive reports whether redaction keys are live. Queued literals are
// bundle-wide, so redaction stays available in both the transcript and detail views.
func (m tview) redactActive() bool {
	return m.redactMode && !m.live
}

// handleRedactKey processes redaction keys and input. ok reports the key was consumed.
func (m tview) handleRedactKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.redactActive() {
		return nil, false
	}
	if m.redact.listActive {
		switch {
		case m.matches(msg, transcriptKeys.Back):
			m.redact.listActive = false
			return nil, true
		case m.matches(msg, redactListKeys.Down):
			m.redact.listCursor = min(m.redact.listCursor+1, max(0, len(m.redact.literals)-1))
			return nil, true
		case m.matches(msg, redactListKeys.Up):
			m.redact.listCursor = max(0, m.redact.listCursor-1)
			return nil, true
		case m.matches(msg, redactListKeys.Remove):
			if i := m.redact.listCursor; i < len(m.redact.literals) {
				m.redact.literals = append(m.redact.literals[:i], m.redact.literals[i+1:]...)
				m.redact.listCursor = max(0, min(i, len(m.redact.literals)-1))
			}
			return nil, true
		case m.matches(msg, transcriptKeys.Redact): // d: add another, then return here
			m.redact.listActive, m.redact.listReturn = false, true
			m.redact.inputActive = true
			m.redact.input = newRedactInput()
			return m.redact.input.Focus(), true
		}
		return nil, true
	}
	if m.redact.inputActive {
		return m.handleRedactInput(msg)
	}
	switch {
	case m.matches(msg, transcriptKeys.Redact):
		m.redact.inputActive = true
		m.redact.input = newRedactInput()
		return m.redact.input.Focus(), true
	case m.matches(msg, transcriptKeys.RedactList):
		m.redact.listActive = true
		m.redact.listCursor = 0
		return nil, true
	case m.matches(msg, transcriptKeys.RedactSave):
		if len(m.redact.literals) == 0 {
			m.c.setFlash("no redactions queued")
			return nil, true
		}
		return m.redactPrepareCmd(), true
	}
	return nil, false
}

func (m tview) handleRedactInput(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch msg.Code {
	case tea.KeyEnter:
		if s := m.redact.input.Value(); s != "" {
			m.redact.literals = append(m.redact.literals, s)
			m.c.setFlash("redaction queued")
		}
		m.closeRedactInput()
		if m.redact.listReturn { // came from the list — reopen it on the new entry
			m.redact.listActive, m.redact.listReturn = true, false
			m.redact.listCursor = max(0, len(m.redact.literals)-1)
		}
		return nil, true
	case tea.KeyEscape:
		m.closeRedactInput()
		if m.redact.listReturn {
			m.redact.listActive, m.redact.listReturn = true, false
		}
		return nil, true
	}
	var cmd tea.Cmd
	m.redact.input, cmd = m.redact.input.Update(msg)
	return cmd, true
}

func (m tview) closeRedactInput() {
	m.redact.inputActive = false
	m.redact.input.SetValue("")
	m.redact.input.Blur()
}
