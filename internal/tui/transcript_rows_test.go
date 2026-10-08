package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func rtThink(id string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryThinking, Text: "pondering " + id}
}

func rtTool(id string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryTool, ToolName: "Read", InputPreview: "file-" + id}
}

func rtText(id, text string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntryText, Text: text}
}

func rtSpawn(id string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntrySubagent, ToolName: "spawn_agent",
		Subagents: []transcript.Subagent{{ID: "agent-" + id, Type: "explorer"}}}
}

func rtWait(id string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntrySubagent, ToolName: "wait_agent",
		Subagents: []transcript.Subagent{{ID: "agent-x", Name: "Volta"}}}
}

func rtTeammate(id string) transcript.Entry {
	return transcript.Entry{ID: id, Kind: transcript.EntrySubagent, Text: "hello",
		Subagents: []transcript.Subagent{{Name: "bob", IsTeammate: true}}}
}

// rowsDesc describes rows compactly: E:<entry id>, S:<key>:<thinking>:<tools>,
// H:<key>, F:<key>.
func rowsDesc(es []transcript.Entry, rows []displayRow) []string {
	var out []string
	for _, r := range rows {
		switch r.kind {
		case entryRow:
			out = append(out, "E:"+es[r.entry].ID)
		case runSummary:
			out = append(out, fmt.Sprintf("S:%s:%d:%d", r.run.key, r.run.thinking, r.run.tools))
		case runHead:
			out = append(out, "H:"+r.run.key)
		case runFoot:
			out = append(out, "F:"+r.run.key)
		}
	}
	return out
}

func assertRows(t *testing.T, es []transcript.Entry, rows []displayRow, want ...string) {
	t.Helper()
	if got := rowsDesc(es, rows); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func runFixture() []transcript.Entry {
	return []transcript.Entry{
		userEntry("u1", "fix it"),
		rtThink("t1"), rtTool("x1"), rtTool("x2"),
		rtText("o1", "done"),
		userEntry("u2", "thanks"),
	}
}

func TestBuildRowsFoldsRunsCollapsed(t *testing.T) {
	es := runFixture()
	assertRows(t, es, buildRows(es, false, nil), "E:u1", "S:t1:1:2", "E:o1", "E:u2")
}

func TestBuildRowsExpanded(t *testing.T) {
	es := runFixture()
	assertRows(t, es, buildRows(es, true, nil),
		"E:u1", "H:t1", "E:t1", "E:x1", "E:x2", "F:t1", "E:o1", "E:u2")
}

func TestBuildRowsOverridesBeatVerbose(t *testing.T) {
	es := runFixture()
	assertRows(t, es, buildRows(es, true, map[string]bool{"t1": false}), "E:u1", "S:t1:1:2", "E:o1", "E:u2")
	assertRows(t, es, buildRows(es, false, map[string]bool{"t1": true}),
		"E:u1", "H:t1", "E:t1", "E:x1", "E:x2", "F:t1", "E:o1", "E:u2")
}

func TestBuildRowsSpawnSplitsAgentRefStays(t *testing.T) {
	es := []transcript.Entry{rtTool("a"), rtSpawn("s"), rtTool("b"), rtWait("w"), rtThink("c")}
	assertRows(t, es, buildRows(es, false, nil), "S:a:0:1", "E:s", "S:b:1:2")
}

func TestBuildRowsTeammateSplits(t *testing.T) {
	es := []transcript.Entry{rtTool("a"), rtTeammate("tm"), rtTool("b")}
	assertRows(t, es, buildRows(es, false, nil), "S:a:0:1", "E:tm", "S:b:0:1")
}

func TestBuildRowsSingleEntryRun(t *testing.T) {
	es := []transcript.Entry{userEntry("u", "hi"), rtTool("x")}
	assertRows(t, es, buildRows(es, false, nil), "E:u", "S:x:0:1")
}

func TestBuildRowsRunRange(t *testing.T) {
	es := runFixture()
	rows := buildRows(es, false, nil)
	if r := rows[1].run; r.first != 1 || r.last != 3 {
		t.Errorf("run range = [%d,%d], want [1,3]", r.first, r.last)
	}
}

func TestRunCounts(t *testing.T) {
	th, tl := Icon.Thinking.Glyph, Icon.Tool.Ok.Glyph
	for _, c := range []struct {
		thinking, tools int
		want            string
	}{
		{2, 5, th + " 2 · " + tl + " 5"},
		{0, 1, tl + " 1"},
		{3, 0, th + " 3"},
		{1, 1, th + " 1 · " + tl + " 1"},
	} {
		if got := ansi.Strip(runCounts(c.thinking, c.tools)); got != c.want {
			t.Errorf("runCounts(%d,%d) = %q, want %q", c.thinking, c.tools, got, c.want)
		}
	}
}

// runModel is a live transcript holding runFixture, focused, the cursor on top.
func runModel(verbose bool) model {
	m := deltaModel()
	m.height = 40
	m.verboseTranscript = verbose
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.entries = runFixture()
		t.transcript.cursor, t.transcript.scroll = 0, 0
	})
	return withFocus(m, mainPane)
}

func layoutText(m model) string {
	lines, _ := tvOf(&m).layoutEntries()
	return ansi.Strip(strings.Join(lines, "\n"))
}

func TestRunRowsRender(t *testing.T) {
	m := runModel(false)
	out := layoutText(m)
	if !strings.Contains(out, Icon.Collapsed.Glyph+"  "+Icon.Thinking.Glyph+" 1 · "+Icon.Tool.Ok.Glyph+" 2") {
		t.Errorf("collapsed run should show its summary:\n%s", out)
	}
	if strings.Contains(out, "thinking") || strings.Contains(out, "tools") {
		t.Errorf("run summary should use icons, not words:\n%s", out)
	}
	if strings.Contains(out, "Thinking") || strings.Contains(out, "file-x1") {
		t.Errorf("collapsed run should hide its entries:\n%s", out)
	}
	m = runModel(true)
	out = layoutText(m)
	for _, want := range []string{Icon.Expanded.Glyph + "  " + Icon.Thinking.Glyph + " 1 · " + Icon.Tool.Ok.Glyph + " 2", "Thinking", "file-x1", "file-x2"} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded run missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, Icon.Collapsed.Glyph) {
		t.Errorf("expanded run should not show the summary:\n%s", out)
	}
	counts := Icon.Thinking.Glyph + " 1 · " + Icon.Tool.Ok.Glyph + " 2"
	head, foot := Icon.Expanded.Glyph+"  "+counts, Icon.Collapse.Glyph+"  "+counts
	if strings.Count(out, head) != 1 || strings.Count(out, foot) != 1 || strings.Contains(out, "collapse") {
		t.Errorf("want one head %q (chevron down) and one foot %q (chevron up), no \"collapse\" text:\n%s", head, foot, out)
	}
}

func TestRunRowsCarryTheCursorGutter(t *testing.T) {
	m := runModel(false)
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 1 })
	lines, first := tvOf(&m).layoutEntries()
	if got := ansi.Strip(lines[first[1]]); !strings.HasPrefix(got, GlyphAccentBarFocused+" "+Icon.Collapsed.Glyph) {
		t.Errorf("selected summary row = %q, want the accent gutter then ▸", got)
	}
	if sep := lines[first[1]-1]; sep != "" {
		t.Errorf("a blank line should separate rows, got %q", sep)
	}
}

func cursorOf(m model) int { return trOf(m).transcript.cursor }

func TestCursorWalksControlRows(t *testing.T) {
	m := runModel(false)
	m, _ = upd(m, keyMsg("j"))
	if cursorOf(m) != 1 {
		t.Fatalf("j from the top: cursor = %d, want 1 (the summary row)", cursorOf(m))
	}
	m, _ = upd(m, keyMsg("j"))
	if cursorOf(m) != 2 {
		t.Fatalf("j: cursor = %d, want 2 (the output after the run)", cursorOf(m))
	}
}

func TestExpandAndCollapseRunByKeys(t *testing.T) {
	m := runModel(false)
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 1 })
	m, _ = upd(m, keyMsg("l"))
	if !strings.Contains(layoutText(m), Icon.Expanded.Glyph) || cursorOf(m) != 1 {
		t.Fatalf("l on the summary should expand the run, cursor on its head (got %d)", cursorOf(m))
	}
	m, _ = upd(m, keyMsg("h"))
	if strings.Contains(layoutText(m), Icon.Expanded.Glyph) || cursorOf(m) != 1 {
		t.Fatalf("h on the head should collapse the run, cursor on its summary (got %d)", cursorOf(m))
	}
	m, _ = upd(m, keyMsg("enter"))
	if !strings.Contains(layoutText(m), Icon.Expanded.Glyph) {
		t.Fatal("<CR> on the summary should expand the run")
	}
	if trOf(m).historyView != histTranscript {
		t.Fatal("<CR> on a control row must not drill")
	}
	m, _ = upd(m, keyMsg("enter"))
	if strings.Contains(layoutText(m), Icon.Expanded.Glyph) {
		t.Fatal("<CR> on the head should collapse the run")
	}
	// l on the head does nothing: the run stays expanded.
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, keyMsg("l"))
	if !strings.Contains(layoutText(m), Icon.Expanded.Glyph) || cursorOf(m) != 1 {
		t.Fatalf("l on the head should leave the run expanded (cursor %d)", cursorOf(m))
	}
}

func TestCollapseFromFootLandsOnSummary(t *testing.T) {
	m := runModel(false)
	m.height = 12
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.runs["t1"] = true
		t.transcript.cursor = 5 // the foot row
	})
	tv := tvOf(&m)
	tv.ensureEntryVisible()
	m, _ = upd(m, keyMsg("h"))
	if cursorOf(m) != 1 {
		t.Fatalf("collapse from the foot: cursor = %d, want 1 (the summary)", cursorOf(m))
	}
	if !tvOf(&m).cursorVisible() {
		t.Error("the summary row should be visible after collapsing from the foot")
	}
}

func TestEntryInsideRunKeepsItsOwnFold(t *testing.T) {
	m := runModel(true)
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 2 }) // the thinking entry
	m, _ = upd(m, keyMsg("l"))
	if !strings.Contains(layoutText(m), "pondering t1") {
		t.Fatalf("l on an entry inside a run should expand the entry:\n%s", layoutText(m))
	}
	m, _ = upd(m, keyMsg("h"))
	out := layoutText(m)
	if strings.Contains(out, "pondering t1") || !strings.Contains(out, Icon.Expanded.Glyph) {
		t.Fatalf("h on the entry should fold the entry, not the run:\n%s", out)
	}
}

func TestPromptJumpsSkipControlRows(t *testing.T) {
	m := runModel(false)
	m, _ = upd(m, keyMsg("}"))
	if cursorOf(m) != 3 {
		t.Fatalf("} from the top: cursor = %d, want 3 (u2)", cursorOf(m))
	}
	m, _ = upd(m, keyMsg("{"))
	if cursorOf(m) != 0 {
		t.Fatalf("{: cursor = %d, want 0 (u1)", cursorOf(m))
	}
	m = runModel(true)
	m, _ = upd(m, keyMsg("}"))
	if cursorOf(m) != 7 {
		t.Fatalf("} over an expanded run: cursor = %d, want 7 (u2)", cursorOf(m))
	}
}

func TestClickExpandsAndCollapsesRun(t *testing.T) {
	m := withMouse(runModel(false))
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x+4, y)
	if !strings.Contains(layoutText(m), Icon.Expanded.Glyph) || cursorOf(m) != 1 {
		t.Fatalf("a click on the summary should expand the run (cursor %d)", cursorOf(m))
	}
	x, y = itemCell(t, m, regMain, 5) // the foot
	m, _ = click(m, x+4, y)
	if strings.Contains(layoutText(m), Icon.Expanded.Glyph) || cursorOf(m) != 1 {
		t.Fatalf("a click on the foot should collapse the run, cursor on the summary (got %d)", cursorOf(m))
	}
}

func TestClickFoldMarkerExpandsRun(t *testing.T) {
	m := withMouse(runModel(false))
	m.View()
	x, y, found := 0, 0, false
	for _, a := range m.hits.areas {
		for _, z := range a.zones {
			if z.target.kind == hitFold && z.target.index == 1 {
				x, y, found = a.rect.Min.X+z.rect.Min.X, a.rect.Min.Y+z.rect.Min.Y, true
			}
		}
	}
	if !found {
		t.Fatal("the summary row should carry a fold zone over its ▸")
	}
	m, _ = click(m, x, y)
	if !strings.Contains(layoutText(m), Icon.Expanded.Glyph) {
		t.Fatal("a click on the ▸ should expand the run")
	}
}

func TestDrillOnEntryRowInsideRun(t *testing.T) {
	m := runModel(true)
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 3 }) // x1
	m, _ = upd(m, keyMsg("enter"))
	tr := trOf(m)
	if tr.historyView != histDetail || len(tr.transcript.detailStack) != 1 ||
		tr.transcript.detailStack[0].items[0].ID != "x1" {
		t.Fatalf("<CR> on an entry row inside a run should drill into it")
	}
}

// runDeltaModel is a live transcript ending in a run, which deltas grow.
func runDeltaModel(verbose bool) model {
	m := deltaModel()
	m.verboseTranscript = verbose
	es := append(userEntries(20), rtTool("r1"))
	return withTr(m, func(t *transcriptComp) { t.transcript.entries = es })
}

func growRun(from int, ids ...string) transcriptDeltaMsg {
	var es []transcript.Entry
	for _, id := range ids {
		es = append(es, rtTool(id))
	}
	return transcriptDeltaMsg{ref: subRef{subID: "x", sessionID: "s1"}, delta: api.TranscriptDelta{FromIndex: from, Entries: es}}
}

func TestFollowStaysOnLastRowAsRunGrows(t *testing.T) {
	m := runDeltaModel(false)
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.cursor = 20 // the run's summary, the last row
		t.transcript.scroll = tvOf(&m).maxScroll()
	})
	m, _ = upd(m, growRun(21, "r2"))
	if cursorOf(m) != 20 {
		t.Fatalf("cursor = %d, want 20 (still the last row)", cursorOf(m))
	}
	if !strings.Contains(layoutText(m), Icon.Collapsed.Glyph+"  "+Icon.Tool.Ok.Glyph+" 2") {
		t.Fatalf("summary should count the new tool:\n%s", layoutText(m))
	}
	m, _ = upd(m, transcriptDeltaMsg{ref: subRef{subID: "x", sessionID: "s1"},
		delta: api.TranscriptDelta{FromIndex: 22, Entries: []transcript.Entry{userEntry("u-new", "next")}}})
	if cursorOf(m) != 21 {
		t.Fatalf("cursor = %d, want 21 (the new last row)", cursorOf(m))
	}
	if trOf(m).transcript.scroll != tvOf(&m).maxScroll() {
		t.Error("following view should stay at the bottom")
	}
}

func TestRestoreByRowIdentityAcrossGrowingRun(t *testing.T) {
	m := runDeltaModel(true)
	// Rows: 20 users, head(20), r1(21), foot(22). Cursor on the foot, scrolled up.
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor, t.transcript.scroll = 22, 0 })
	m, _ = upd(m, growRun(21, "r2"))
	if cursorOf(m) != 23 {
		t.Fatalf("cursor = %d, want 23 (the same foot row, moved down by the new entry)", cursorOf(m))
	}
	if trOf(m).transcript.scroll != 0 {
		t.Error("a scrolled-up view should stay put")
	}
	// The head row keeps its index.
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 20 })
	m, _ = upd(m, growRun(22, "r3"))
	if cursorOf(m) != 20 {
		t.Fatalf("cursor = %d, want 20 (the head row)", cursorOf(m))
	}
}

func TestRunOverridesSurviveRefresh(t *testing.T) {
	m := runModel(false)
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 1 })
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, transcriptMsg{id: "s1", entries: runFixture()})
	if !strings.Contains(layoutText(m), Icon.Expanded.Glyph) {
		t.Fatal("an expanded run should stay expanded across a refresh")
	}
}

func TestToggleVerboseTranscriptCommand(t *testing.T) {
	m := runModel(false)
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleVerbose.name))
	if !m.verboseTranscript || m.flash != "verbose transcript on" {
		t.Fatalf("verbose=%v flash=%q, want on", m.verboseTranscript, m.flash)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, Icon.Expanded.Glyph) {
		t.Fatalf("the open transcript should re-render expanded:\n%s", out)
	}
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleVerbose.name))
	if m.verboseTranscript || m.flash != "verbose transcript off" {
		t.Fatalf("verbose=%v flash=%q, want off", m.verboseTranscript, m.flash)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, Icon.Collapsed.Glyph+"  "+Icon.Thinking.Glyph+" 1") {
		t.Fatalf("the open transcript should re-render collapsed:\n%s", out)
	}
}

// runFrameModel drills into an inline subagent trace holding a run.
func runFrameModel() tview {
	return detailTestModel(traceFixture("Explore",
		userEntry("fu", "explore"), rtThink("ft"), rtTool("fx1"), rtTool("fx2"), rtText("fo", "found it")))
}

func frameText(m tview) string {
	lines, _, _ := m.frameLines(m.topFrame(), m.c.m.transcriptWidth())
	return ansi.Strip(strings.Join(lines, "\n"))
}

func TestFrameRunsCollapsedByDefault(t *testing.T) {
	m := runFrameModel()
	out := frameText(m)
	if !strings.Contains(out, Icon.Collapsed.Glyph+"  "+Icon.Thinking.Glyph+" 1 · "+Icon.Tool.Ok.Glyph+" 2") || strings.Contains(out, "file-fx1") {
		t.Fatalf("a trace frame should fold its run:\n%s", out)
	}
	first, _ := m.frameItemStarts(m.topFrame(), m.c.m.transcriptWidth())
	if len(first) != 3 {
		t.Errorf("frame rows = %d, want 3 (prompt, summary, output)", len(first))
	}
}

func TestFrameRunExpandCollapseByKeys(t *testing.T) {
	m := runFrameModel()
	f := m.topFrame()
	k := tea.KeyPressMsg{}
	m.actDetailDown(k)
	if f.cursor != 1 {
		t.Fatalf("down: cursor = %d, want 1 (the summary)", f.cursor)
	}
	m.actDetailExpand(k)
	if !strings.Contains(frameText(m), Icon.Expanded.Glyph+"  "+Icon.Thinking.Glyph+" 1 · "+Icon.Tool.Ok.Glyph+" 2") || f.cursor != 1 {
		t.Fatalf("l on the summary should expand the run, cursor on its head (got %d):\n%s", f.cursor, frameText(m))
	}
	m.actDetailCollapse(k)
	if strings.Contains(frameText(m), Icon.Expanded.Glyph) || f.cursor != 1 {
		t.Fatalf("h on the head should collapse the run (cursor %d)", f.cursor)
	}
	m.actDetailDrill(k)
	if len(m.transcript.detailStack) != 1 || !strings.Contains(frameText(m), Icon.Expanded.Glyph) {
		t.Fatal("<CR> on the summary should expand the run, not drill")
	}
	f.cursor = 5 // the foot
	m.actDetailDrill(k)
	if len(m.transcript.detailStack) != 1 || strings.Contains(frameText(m), Icon.Expanded.Glyph) || f.cursor != 1 {
		t.Fatalf("<CR> on the foot should collapse the run onto its summary (cursor %d)", f.cursor)
	}
	m.clickItem(1, false)
	if !strings.Contains(frameText(m), Icon.Expanded.Glyph) {
		t.Fatal("a click on the summary should expand the run")
	}
	f.cursor = 3 // fx1, inside the run
	m.actDetailDrill(k)
	if len(m.transcript.detailStack) != 2 || m.topFrame().items[0].ID != "fx1" {
		t.Fatal("<CR> on an entry row inside a run should drill")
	}
}

// streamedFrame drills into a live subagent and returns a delta deliverer.
func streamedFrame(t *testing.T) (*model, func(from int, es ...transcript.Entry)) {
	t.Helper()
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentEntry())
	m, cmd := upd(m, keyMsg("enter"))
	var sub transcriptDeltaMsg
	for _, msg := range execCmd(cmd) {
		if d, ok := msg.(transcriptDeltaMsg); ok {
			sub = d
		}
	}
	if sub.ref.agentID == "" {
		t.Fatal("setup: no subagent subscription")
	}
	mp := &m
	return mp, func(from int, es ...transcript.Entry) {
		d := sub
		d.delta.SubID, d.delta.FromIndex, d.delta.Entries = d.ref.subID, from, es
		*mp, _ = upd(*mp, d)
	}
}

func frameOf(m model) detailFrame { return trOf(m).transcript.detailStack[0] }

func TestFrameFollowsOnLastRow(t *testing.T) {
	m, deliver := streamedFrame(t)
	deliver(0, rtText("s0", "step 0"), rtTool("r1"))
	if f := frameOf(*m); f.cursor != 1 {
		t.Fatalf("first load: cursor = %d, want 1 (the run's summary, the last row)", f.cursor)
	}
	deliver(2, rtTool("r2"))
	if f := frameOf(*m); f.cursor != 1 {
		t.Fatalf("a growing run: cursor = %d, want 1 (still the last row)", f.cursor)
	}
	if !strings.Contains(viewText(*m), Icon.Collapsed.Glyph+"  "+Icon.Tool.Ok.Glyph+" 2") {
		t.Fatalf("the summary should count the new tool:\n%s", viewText(*m))
	}
	deliver(3, rtText("s3", "step 3"))
	if f := frameOf(*m); f.cursor != 2 {
		t.Errorf("cursor = %d, want 2 (the new last row)", f.cursor)
	}
}

func TestFrameRestoresRowAcrossGrowingRun(t *testing.T) {
	m, deliver := streamedFrame(t)
	var es []transcript.Entry
	for i := range 30 {
		es = append(es, rtText(fmt.Sprint(i), fmt.Sprint("step ", i)))
	}
	deliver(0, append(es, rtTool("r1"))...)
	// Rows: 30 steps, head(30), r1(31), foot(32). Cursor on the foot, scrolled up.
	*m = withTr(*m, func(t *transcriptComp) {
		f := &t.transcript.detailStack[0]
		f.runs = map[string]bool{"r1": true}
		f.cursor, f.scroll = 32, 0
	})
	deliver(31, rtTool("r2"))
	if f := frameOf(*m); f.cursor != 33 || f.scroll != 0 {
		t.Fatalf("cursor=%d scroll=%d, want 33 (the same foot row) and 0", f.cursor, f.scroll)
	}
}

func TestToggleVerboseReachesFrames(t *testing.T) {
	m, deliver := streamedFrame(t)
	deliver(0, rtText("s0", "step 0"), rtTool("r1"))
	*m, _ = upd(*m, cmdMsg(projectsKeys.ToggleVerbose.name))
	if out := viewText(*m); !strings.Contains(out, Icon.Expanded.Glyph+"  "+Icon.Tool.Ok.Glyph+" 1") {
		t.Fatalf("the open frame should re-render expanded:\n%s", out)
	}
	if f := frameOf(*m); f.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (the head of the run the summary folded)", f.cursor)
	}
}

// Expanding a run near the bottom of the screen scrolls its entries into view.
func TestExpandRevealsRun(t *testing.T) {
	var es []transcript.Entry
	for i := range 20 {
		es = append(es, transcript.Entry{ID: fmt.Sprint("p", i), Kind: transcript.EntryText, Text: fmt.Sprint("prose ", i)})
	}
	for i := range 4 {
		es = append(es, transcript.Entry{ID: fmt.Sprint("t", i), Kind: transcript.EntryTool, ToolName: "Bash", ToolID: fmt.Sprint("tu", i), InputPreview: fmt.Sprint("cmd-", i)})
	}
	m := deltaModel()
	m.height = 20
	m = withFocus(m, mainPane)
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.entries = es
		t.transcript.cursor = 20 // the run's summary, the last row
	})
	m = withTr(m, func(t *transcriptComp) { t.transcript.scroll = tvOver(&m, *t).maxScroll() })
	m, _ = upd(m, keyMsg("l"))
	v := tvOf(&m)
	rows := v.displayRows()
	head, foot, ok := runEdges(rows, "t0")
	if !ok {
		t.Fatal("run did not expand")
	}
	lines, first := v.layoutEntries()
	_, end := v.entrySpan(foot, first, len(lines))
	start, _ := v.entrySpan(head, first, len(lines))
	scroll, h := trOf(m).transcript.scroll, v.viewportHeight()
	if end > scroll+h || start < scroll {
		t.Errorf("run lines [%d,%d) not within viewport [%d,%d)", start, end, scroll, scroll+h)
	}
}
