package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// TestSmokeRealTranscript exercises parser + entry folding + viewer against a
// real on-disk transcript; runs only when ARGUS_TRANSCRIPT is set.
func TestSmokeRealTranscript(t *testing.T) {
	path := os.Getenv("ARGUS_TRANSCRIPT")
	if path == "" {
		t.Skip("set ARGUS_TRANSCRIPT to run")
	}
	view, err := adapters.Default().ReadTranscriptView(path)
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	t.Logf("built %d entries", len(view.Entries))

	m := bareTv()
	initTheme(true)
	initIcons()
	initStyles()
	m.c.m.hasDark = true
	m.c.m.render.jsonHL = newCodeHighlighter(true, "json")
	m.transcript.entries = view.Entries

	lines, first := m.layoutEntries()
	t.Logf("collapsed layout: %d lines, %d entry offsets", len(lines), len(first))

	setAllExpanded(m, true)
	linesExp, _ := m.layoutEntries()
	t.Logf("expanded layout: %d lines", len(linesExp))

	m.transcript.cursor = max(0, len(m.transcript.entries)-1)
	m.ensureEntryVisible()
	if m.transcript.scroll < 0 {
		t.Errorf("negative scroll after ensureEntryVisible: %d", m.transcript.scroll)
	}

	// Print the top-of-transcript window for eyeballing.
	setAllExpanded(m, false)
	m.transcript.cursor, m.transcript.scroll = 0, 0
	out := m.transcriptBody()
	preview := strings.SplitN(out, "\n", 45)
	t.Logf("\n%s", strings.Join(preview, "\n"))

	// Dump the first tool entry collapsed then expanded for eyeballing.
	for i, e := range m.transcript.entries {
		if e.Kind != transcript.EntryTool {
			continue
		}
		m.transcript.expanded = map[string]bool{}
		t.Logf("tool #%d collapsed:\n%s", i, m.renderEntry(i, true))
		m.transcript.expanded[e.ID] = true
		t.Logf("tool #%d expanded:\n%s", i, m.renderEntry(i, false))
		break
	}

	// If any entry is a linked subagent, dump its detail (trace) view.
	for i, e := range m.transcript.entries {
		if s, ok := soleSubagent(e); !ok || len(s.Trace) == 0 {
			continue
		}
		m.transcript.cursor = i
		m.enterDetail()
		detail := strings.SplitN(m.detailBody(), "\n", 60)
		t.Logf("detail (entry #%d, with subagent trace):\n%s", i, strings.Join(detail, "\n"))
		break
	}
}
