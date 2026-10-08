package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// freshLines renders the transcript without the row cache, as the reference
// the cached layout must match.
func freshLines(m tview) []string {
	var lines []string
	for i, r := range m.displayRows() {
		if i > 0 {
			lines = append(lines, "")
		}
		block := centerBlock(m.renderRow(r, i == m.transcript.cursor), m.c.m.containerWidth(), m.c.m.bodyWidth())
		lines = append(lines, strings.Split(block, "\n")...)
	}
	return lines
}

func assertLayoutFresh(t *testing.T, m tview, step string) {
	t.Helper()
	got, _ := m.layoutEntries()
	if want := freshLines(m); !slices.Equal(got, want) {
		t.Errorf("%s: cached layout differs from a fresh render:\n got:\n%s\nwant:\n%s",
			step, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func mixedEntries(n int) []transcript.Entry {
	md := "## Heading\n\nSome **bold** text with `code`:\n\n- one\n- two\n"
	out := make([]transcript.Entry, 0, n)
	for i := range n {
		switch i % 4 {
		case 0:
			out = append(out, transcript.Entry{ID: fmt.Sprintf("u%d", i), Kind: transcript.EntryUser, Text: fmt.Sprintf("do thing %d", i)})
		case 1:
			out = append(out, transcript.Entry{ID: fmt.Sprintf("t%d", i), Kind: transcript.EntryTool, ToolName: "Bash", InputPreview: "ls"})
		case 2:
			out = append(out, transcript.Entry{ID: fmt.Sprintf("x%d", i), Kind: transcript.EntryText, Text: md})
		default:
			out = append(out, transcript.Entry{ID: fmt.Sprintf("e%d", i), Kind: transcript.EntryTurnEnd, ModelName: "opus"})
		}
	}
	return out
}

func setAllExpanded(m tview, on bool) {
	for i := range m.transcript.entries {
		m.setExpanded(i, on)
	}
}

// The cached layout tracks every input an entry renders from.
func TestLayoutCacheTracksRenderInputs(t *testing.T) {
	mm := withFocus(withView(testModel(), viewSession), sessionDock)
	m := tvOf(&mm)
	m.transcript.entries = mixedEntries(6)
	assertLayoutFresh(t, m, "initial")

	m.transcript.cursor = 1
	assertLayoutFresh(t, m, "cursor moved")

	m.setExpanded(1, true)
	assertLayoutFresh(t, m, "entry expanded")

	mm = withFocus(mm, mainPane)
	assertLayoutFresh(t, m, "history focused")

	m.c.m.width = 120
	assertLayoutFresh(t, m, "width changed")

	setAllExpanded(m, true)
	assertLayoutFresh(t, m, "all expanded")
}

// A delta that rewrites the last entry in place (same id) shows the new content.
func TestLayoutCacheDropsEntriesReplacedByDelta(t *testing.T) {
	m := deltaModel()
	tvOf(&m).layoutEntries()

	upd := transcript.Entry{ID: "c19", Kind: transcript.EntryUser, Text: "rewritten"}
	res, _ := m.Update(transcriptDeltaMsg{
		ref:   subRef{subID: "x", sessionID: "s1"},
		delta: api.TranscriptDelta{FromIndex: 19, Entries: []transcript.Entry{upd}},
	})
	assertLayoutFresh(t, tvIn(res.(model)), "delta")
}

// A full reload that keeps entry ids shows the new content.
func TestLayoutCacheDropsEntriesOnFullReload(t *testing.T) {
	reloaded := userEntries(20)
	reloaded[0].Text = "reloaded"
	history := testModel()
	history.width, history.height = 80, 10
	history = withEntries(withView(history, viewHistoryTranscript), userEntries(20))

	for _, tc := range []struct {
		name string
		m    model
		msg  tea.Msg
	}{
		{"live", deltaModel(), transcriptMsg{id: "s1", entries: reloaded}},
		{"history", history, histTranscriptMsg{addr: trOf(history).history.addr(), entries: reloaded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.m
			tvOf(&m).layoutEntries()
			res, _ := m.Update(tc.msg)
			assertLayoutFresh(t, tvIn(res.(model)), tc.name)
		})
	}
}

// Rebinding the stream to another transcript shows that transcript's entries.
func TestLayoutCacheDropsEntriesOnRebind(t *testing.T) {
	mm := deltaModel()
	m := tvOf(&mm)
	m.layoutEntries()

	other := userEntries(20)
	other[5].Text = "other session"
	ref := subRef{subID: "y", sessionID: "s2", cacheKey: "s2"}
	m.c.m.transcriptCache[ref.key()] = cachedTranscript{entries: other}
	m.bindStream(ref)
	assertLayoutFresh(t, m, "rebind")
}

func BenchmarkTranscriptScroll(b *testing.B) {
	m := testModel()
	m.width, m.height = 160, 50
	m = withView(m, viewSession)
	v := tvOf(&m)
	v.transcript.entries = mixedEntries(200)
	v.transcript.cursor = 100
	v.layoutEntries()
	for b.Loop() {
		v.actPromptNext(tea.KeyPressMsg{})
		v.put()
		_ = m.View()
	}
}

// An expanded tool's cached row refreshes when its fetched body arrives.
func TestLayoutCacheRefreshesOnToolBody(t *testing.T) {
	m := withVerbose(deltaModel())
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.entries = []transcript.Entry{{ID: "1.0", Kind: transcript.EntryTool, ToolName: "UnknownTool", ToolID: "tu1", InputPreview: "x"}}
		t.transcript.cursor = 0
		t.transcript.expanded["1.0"] = true
		t.toolBodies["tu1"] = toolBodyEntry{loading: true}
	})
	if lines, _ := tvOf(&m).layoutEntries(); !strings.Contains(strings.Join(lines, "\n"), "loading…") {
		t.Fatalf("setup: expanded tool should show the loading placeholder:\n%s", strings.Join(lines, "\n"))
	}
	res, _ := m.Update(toolDetailMsg{owner: trOf(m).owner(), toolID: "tu1", detail: api.ToolDetail{Result: "fetched-body"}})
	v := tvIn(res.(model))
	assertLayoutFresh(t, v, "tool body")
	if lines, _ := v.layoutEntries(); !strings.Contains(strings.Join(lines, "\n"), "fetched-body") {
		t.Errorf("cached row should show the fetched body:\n%s", strings.Join(lines, "\n"))
	}
}
