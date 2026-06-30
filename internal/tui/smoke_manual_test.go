package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
)

// TestSmokeRealTranscript exercises parser + chunk builder + viewer against a
// real on-disk transcript; runs only when ARGUS_TRANSCRIPT is set.
func TestSmokeRealTranscript(t *testing.T) {
	path := os.Getenv("ARGUS_TRANSCRIPT")
	if path == "" {
		t.Skip("set ARGUS_TRANSCRIPT to run")
	}
	view, err := claudecode.ReadTranscriptView(path)
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	t.Logf("built %d chunks", len(view.Chunks))

	m := testModel()
	initTheme(true)
	initIcons()
	initStyles()
	m.hasDark = true
	m.transcript.jsonHL = newJSONHighlighter(true)
	m.transcript.chunks = view.Chunks

	// Collapsed layout.
	lines, first := m.layoutChunks()
	t.Logf("collapsed layout: %d lines, %d chunk offsets", len(lines), len(first))

	// Expand-all layout (exercise item rows + last-output bodies).
	m.setAllExpanded(true)
	linesExp, _ := m.layoutChunks()
	t.Logf("expanded layout: %d lines", len(linesExp))

	// Navigate to the last chunk and ensure scroll stays valid.
	m.transcript.cursor = max(0, len(m.transcript.chunks)-1)
	m.ensureChunkVisible()
	if m.transcript.scroll < 0 {
		t.Errorf("negative scroll after ensureChunkVisible: %d", m.transcript.scroll)
	}

	// Print the top-of-transcript window for eyeballing.
	m.setAllExpanded(false)
	m.transcript.cursor, m.transcript.scroll = 0, 0
	out := m.transcriptBody()
	preview := strings.SplitN(out, "\n", 45)
	t.Logf("\n%s", strings.Join(preview, "\n"))

	// Dump the first AI card collapsed then expanded for eyeballing.
	for i, c := range m.transcript.chunks {
		if c.Kind != claudecode.ChunkAI {
			continue
		}
		m.transcript.expanded = map[string]bool{}
		t.Logf("AI card #%d collapsed:\n%s", i, m.renderChunk(i, true))
		m.transcript.expanded[c.ID] = true
		t.Logf("AI card #%d expanded:\n%s", i, m.renderChunk(i, false))
		break
	}

	// If any chunk contains a linked subagent, dump its detail (trace) view.
	for i, c := range m.transcript.chunks {
		hasTrace := false
		for _, it := range c.Items {
			if it.Kind == claudecode.ItemSubagent && len(it.Trace) > 0 {
				hasTrace = true
			}
		}
		if !hasTrace {
			continue
		}
		m.transcript.cursor = i
		m.enterDetail()
		detail := strings.SplitN(m.detailBody(), "\n", 60)
		t.Logf("detail (chunk #%d, with subagent trace):\n%s", i, strings.Join(detail, "\n"))
		break
	}
}
