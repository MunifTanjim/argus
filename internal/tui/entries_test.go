package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func sampleEntries() []transcript.Entry {
	return []transcript.Entry{
		{ID: "0", Kind: transcript.EntryUser, Text: "fix the bug", Timestamp: "2026-10-06T14:31:00Z"},
		{ID: "1.0", Kind: transcript.EntryThinking, Text: "reasoning"},
		{ID: "1.1", Kind: transcript.EntryText, Text: "I'll check the handler."},
		{ID: "1.2", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tu1", InputPreview: "go test ./..."},
		{ID: "1.end", Kind: transcript.EntryTurnEnd, ModelName: "Opus 4.8", Usage: transcript.Usage{Output: 12300}, DurationMs: 62000},
	}
}

func liveWith(es []transcript.Entry) model {
	m := testModel()
	m.sessions = map[string]session.Session{"s1": {ID: "s1"}}
	m = openLive(m, "s1")
	return withEntries(m, es)
}

func TestFlatStreamRendersEachEntry(t *testing.T) {
	m := liveWith(sampleEntries())
	out := xansi.Strip(tvOf(&m).transcriptBody())
	for _, want := range []string{"You", "fix the bug", "Thinking…", "I'll check the handler.", "go test ./...", "Opus 4.8", "1m"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "╭") || strings.Contains(out, "╰") {
		t.Errorf("flat stream still draws card borders:\n%s", out)
	}
}

func TestUserBandSpansWidth(t *testing.T) {
	m := liveWith(sampleEntries())
	v := tvOf(&m)
	w := m.transcriptWidth()
	block := v.entryBlock(sampleEntries()[0], false, false, false, false, w)
	for i, l := range strings.Split(block, "\n") {
		if got := xansi.StringWidth(l); got != w {
			t.Errorf("band line %d width = %d, want %d", i, got, w)
		}
	}
}

func TestTurnEndInterrupted(t *testing.T) {
	m := liveWith(nil)
	v := tvOf(&m)
	out := xansi.Strip(v.entryBlock(transcript.Entry{Kind: transcript.EntryTurnEnd, Interrupted: true}, false, false, false, false, 60))
	if !strings.Contains(out, "interrupted") || !strings.Contains(out, GlyphHRule) {
		t.Errorf("footer = %q", out)
	}
}

func TestCursorGutterOnlyOnSelected(t *testing.T) {
	m := liveWith(sampleEntries())
	v := tvOf(&m)
	e := sampleEntries()[2]
	sel := xansi.Strip(v.entryBlock(e, false, true, true, false, 60))
	unsel := xansi.Strip(v.entryBlock(e, false, false, true, false, 60))
	if !strings.HasPrefix(sel, GlyphAccentBarFocused) {
		t.Errorf("selected block = %q, want gutter bar", sel)
	}
	if strings.Contains(unsel, GlyphAccentBarFocused) {
		t.Errorf("unselected block has a gutter bar: %q", unsel)
	}
}

func TestThinkingExpands(t *testing.T) {
	m := liveWith(sampleEntries())
	v := tvOf(&m)
	e := sampleEntries()[1]
	if !v.entryExpandable(e) {
		t.Fatal("thinking with text should be expandable")
	}
	if got := xansi.Strip(v.entryBlock(e, false, false, false, false, 60)); strings.Contains(got, "reasoning") {
		t.Errorf("collapsed thinking shows its text: %q", got)
	}
	if got := xansi.Strip(v.entryBlock(e, true, false, false, false, 60)); !strings.Contains(got, "reasoning") {
		t.Errorf("expanded thinking hides its text: %q", got)
	}
}

func TestEntryCursorMovesPerEntry(t *testing.T) {
	m := liveWith(sampleEntries())
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 0 })
	m, _ = onTr(m, func(v tview) tea.Cmd { return v.actScrollDown(tea.KeyPressMsg{}) })
	if got := trOf(m).transcript.cursor; got != 1 {
		t.Errorf("cursor after j = %d, want 1 (next entry)", got)
	}
}

func TestDrillToolOpensFocusedLeaf(t *testing.T) {
	m := liveWith(sampleEntries())
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 3 })
	m, _ = onTr(m, func(v tview) tea.Cmd { return v.actDrill(tea.KeyPressMsg{}) })
	tr := trOf(m)
	if tr.historyView != histDetail || len(tr.transcript.detailStack) != 1 {
		t.Fatalf("view=%v stack=%d", tr.historyView, len(tr.transcript.detailStack))
	}
	f := tr.transcript.detailStack[0]
	if !f.focused || len(f.items) != 1 || f.items[0].ID != "1.2" {
		t.Errorf("frame = %+v", f)
	}
}

func TestTurnEndNotDetailable(t *testing.T) {
	m := liveWith(sampleEntries())
	if m.detailable(sampleEntries()[4]) {
		t.Error("turn_end should not open a detail")
	}
}

func TestShellRowCollapsedAndExpanded(t *testing.T) {
	m := liveWith(nil)
	v := tvOf(&m)
	e := transcript.Entry{Kind: transcript.EntryShell, Text: "ls", Detail: "Exit code: 1\nboom", IsError: true}
	col := xansi.Strip(v.entryBlock(e, false, false, false, false, 60))
	if !strings.Contains(col, "$ ls") || strings.Contains(col, "Error") {
		t.Errorf("collapsed shell = %q", col)
	}
	exp := xansi.Strip(v.entryBlock(e, true, false, false, false, 60))
	if !strings.Contains(exp, "Error") {
		t.Errorf("expanded shell = %q", exp)
	}
}

func TestTraceFrameListsAllEntries(t *testing.T) {
	trace := []transcript.Entry{
		{ID: "0", Kind: transcript.EntryUser, Text: "explore"},
		{ID: "1.0", Kind: transcript.EntryText, Text: "found it"},
	}
	m := liveWith([]transcript.Entry{{ID: "1.0", Kind: transcript.EntrySubagent, ToolName: "Task",
		Subagents: []transcript.Subagent{{Type: "Explore", HasTrace: true, Trace: trace}}}})
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 0 })
	m, _ = onTr(m, func(v tview) tea.Cmd { return v.actDrill(tea.KeyPressMsg{}) })
	f := trOf(m).transcript.detailStack[0]
	if len(f.items) != 2 || f.items[0].Kind != transcript.EntryUser {
		t.Errorf("trace frame items = %+v", f.items)
	}
}

// A live subagent drilled from the main stream is the root frame and carries
// the subscription; backing out of it must return to the stream.
func TestLiveSubagentRootBackReturnsToStream(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentEntry())
	m = drillSubagent(t, m)
	if tr := trOf(m); tr.historyView != histDetail || len(tr.transcript.detailStack) != 1 || tr.transcript.detailStack[0].subID == "" {
		t.Fatalf("setup: view=%v stack=%+v", tr.historyView, tr.transcript.detailStack)
	}
	m, cmd := upd(m, keyMsg("esc"))
	runCmd(cmd)
	if tr := trOf(m); tr.historyView != histTranscript || len(tr.transcript.detailStack) != 0 {
		t.Errorf("back from the subagent root: view=%v frames=%d, want the stream", tr.historyView, len(tr.transcript.detailStack))
	}
}

// Only a user band after the first entry gets a blank separator; an agent turn's
// rows run consecutively, and spans tile the lines without gaps or overlap.
func TestSeparatorBetweenEntries(t *testing.T) {
	es := append(sampleEntries(), transcript.Entry{ID: "2", Kind: transcript.EntryUser, Text: "again"})
	m := liveWith(es)
	v := tvOf(&m)
	lines, first := v.layoutEntries()
	var blanks []int
	for i, l := range lines {
		if l == "" {
			blanks = append(blanks, i)
		}
	}
	var want []int
	for i := 1; i < len(first); i++ {
		want = append(want, first[i]-1)
	}
	if !reflect.DeepEqual(blanks, want) {
		t.Fatalf("blank lines at %v, want one before each later entry %v", blanks, want)
	}
	prevEnd := 0
	for i := range first {
		start, end := v.entrySpan(i, first, len(lines))
		w := prevEnd
		if i > 0 {
			w++ // the separator
		}
		if start != w || end <= start {
			t.Errorf("span %d = [%d,%d), want start %d and non-empty", i, start, end, w)
		}
		prevEnd = end
	}
	if prevEnd != len(lines) {
		t.Errorf("last span ends at %d, want %d", prevEnd, len(lines))
	}
}

func foldZoneCells(m model) map[int][2]int {
	out := map[int][2]int{}
	for _, a := range m.hits.areas {
		if a.region != regMain {
			continue
		}
		for _, z := range a.zones {
			if z.target.kind == hitFold {
				p := a.rect.Min.Add(z.rect.Min)
				out[z.target.index] = [2]int{p.X, p.Y}
			}
		}
	}
	return out
}

// An entry's leading icon is its fold marker: clicking it selects and toggles
// the entry. Entries that can't expand get no marker zone.
func TestFoldMarkerClickTogglesEntry(t *testing.T) {
	m := withFocus(withMouse(liveWith(sampleEntries())), mainPane)
	lines := strings.Split(xansi.Strip(m.View().Content), "\n")
	zones := foldZoneCells(m)
	for _, i := range []int{1, 3} {
		p, ok := zones[i]
		if !ok {
			t.Fatalf("expandable entry %d has no fold zone (zones %v)", i, zones)
		}
		if cell := xansi.Cut(lines[p[1]], p[0], p[0]+1); strings.TrimSpace(cell) == "" {
			t.Errorf("entry %d: fold zone covers %q, want its icon in %q", i, cell, lines[p[1]])
		}
	}
	for _, i := range []int{0, 2, 4} {
		if _, ok := zones[i]; ok {
			t.Errorf("non-expandable entry %d has a fold zone", i)
		}
	}
	p := zones[1]
	id := sampleEntries()[1].ID
	m, _ = click(m, p[0], p[1])
	if tr := trOf(m); !tr.transcript.expanded[id] || tr.transcript.cursor != 1 {
		t.Fatalf("after click: expanded=%v cursor=%d, want expanded and selected", tr.transcript.expanded[id], tr.transcript.cursor)
	}
	m.View()
	p = foldZoneCells(m)[1]
	m, _ = click(m, p[0], p[1])
	if trOf(m).transcript.expanded[id] {
		t.Error("a second click on the fold marker should collapse the entry")
	}
}

func TestUserBandStripsCR(t *testing.T) {
	m := liveWith(nil)
	v := tvOf(&m)
	out := v.entryBlock(transcript.Entry{Kind: transcript.EntryUser, Text: "one\r\ntwo\r\n"}, false, false, false, false, 60)
	if strings.Contains(out, "\r") {
		t.Errorf("user band kept a carriage return: %q", out)
	}
}

func twoTurns() []transcript.Entry {
	return []transcript.Entry{
		{ID: "0", Kind: transcript.EntryUser, Text: "one"},
		{ID: "1.0", Kind: transcript.EntryText, Text: "a"},
		{ID: "1.end", Kind: transcript.EntryTurnEnd},
		{ID: "2", Kind: transcript.EntryUser, Text: "two"},
		{ID: "3.0", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tu1"},
	}
}

func TestPromptJumps(t *testing.T) {
	m := liveWith(twoTurns())
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 1 })
	m, _ = onTr(m, func(v tview) tea.Cmd { return v.actPromptNext(tea.KeyPressMsg{}) })
	if got := trOf(m).transcript.cursor; got != 3 {
		t.Fatalf("next prompt cursor = %d, want 3", got)
	}
	m, _ = onTr(m, func(v tview) tea.Cmd { return v.actPromptNext(tea.KeyPressMsg{}) })
	if got := trOf(m).transcript.cursor; got != 3 {
		t.Fatalf("no later prompt: cursor = %d, want 3", got)
	}
	m, _ = onTr(m, func(v tview) tea.Cmd { return v.actPromptPrev(tea.KeyPressMsg{}) })
	if got := trOf(m).transcript.cursor; got != 0 {
		t.Fatalf("prev prompt cursor = %d, want 0", got)
	}
}

func TestExpandToolFetchesOnce(t *testing.T) {
	m := liveWith(twoTurns())
	m = withTr(m, func(t *transcriptComp) { t.transcript.cursor = 4 })
	m, cmd := onTr(m, func(v tview) tea.Cmd { return v.actExpand(tea.KeyPressMsg{}) })
	if cmd == nil {
		t.Fatal("expanding a tool should fetch its body")
	}
	if !trOf(m).transcript.expanded["3.0"] {
		t.Fatal("tool not expanded")
	}
	out := xansi.Strip(tvOf(&m).transcriptBody())
	if !strings.Contains(out, "loading…") {
		t.Errorf("expanded tool before body arrives should show loading…:\n%s", out)
	}
	m, cmd = onTr(m, func(v tview) tea.Cmd {
		v.setExpanded(4, false)
		return v.actExpand(tea.KeyPressMsg{})
	})
	if cmd != nil {
		t.Error("a second expand re-fetched the body")
	}
}

func TestClickFoldFetchesToolBody(t *testing.T) {
	m := liveWith(twoTurns())
	m, cmd := onTr(m, func(v tview) tea.Cmd { return v.clickFold(4) })
	if cmd == nil {
		t.Fatal("clicking a tool's fold marker should fetch its body")
	}
	if !trOf(m).transcript.expanded["3.0"] {
		t.Fatal("tool not expanded")
	}
	_, cmd = onTr(m, func(v tview) tea.Cmd { return v.clickFold(4) })
	if cmd != nil {
		t.Error("collapsing fetched a body")
	}
}

func TestUserBandHasVerticalPadding(t *testing.T) {
	m := liveWith(sampleEntries())
	v := tvOf(&m)
	w := m.transcriptWidth()
	lines := strings.Split(xansi.Strip(v.entryBlock(sampleEntries()[0], false, false, false, false, w)), "\n")
	if len(lines) != 4 {
		t.Fatalf("band = %d lines, want 4 (pad, header, text, pad): %q", len(lines), lines)
	}
	for _, i := range []int{0, len(lines) - 1} {
		if strings.TrimSpace(lines[i]) != "" || xansi.StringWidth(lines[i]) != w {
			t.Errorf("padding line %d = %q, want %d blank cells", i, lines[i], w)
		}
	}
	if !strings.Contains(lines[1], "You") || !strings.Contains(lines[2], "fix the bug") {
		t.Errorf("band content = %q", lines)
	}
}

func TestFoldMarkerClickOnYouTogglesLongPrompt(t *testing.T) {
	es := []transcript.Entry{
		{ID: "0", Kind: transcript.EntryUser, Text: strings.Repeat("line\n", 20), Timestamp: "2026-10-06T14:31:00Z"},
		{ID: "1.0", Kind: transcript.EntryText, Text: "ok"},
	}
	m := withFocus(withMouse(liveWith(es)), mainPane)
	m.height = 60
	lines := strings.Split(xansi.Strip(m.View().Content), "\n")
	p, ok := foldZoneCells(m)[0]
	if !ok {
		t.Fatal("long prompt has no fold zone")
	}
	x := strings.Index(lines[p[1]], "You")
	if x < 0 {
		t.Fatalf("no You on the zone's row %q", lines[p[1]])
	}
	x = xansi.StringWidth(lines[p[1]][:x]) // byte offset -> cell
	m, _ = click(m, x, p[1])
	if tr := trOf(m); !tr.transcript.expanded["0"] || tr.historyView != histTranscript {
		t.Fatalf("click on You: expanded=%v view=%v, want expanded in the stream", tr.transcript.expanded["0"], tr.historyView)
	}
	m.View()
	m, _ = click(m, x, p[1])
	if tr := trOf(m); tr.transcript.expanded["0"] || tr.historyView != histTranscript {
		t.Fatalf("second click on You: expanded=%v view=%v, want collapsed in the stream", tr.transcript.expanded["0"], tr.historyView)
	}
}

func TestLongAgentMessageFolds(t *testing.T) {
	long := transcript.Entry{ID: "1.0", Kind: transcript.EntrySubagent, Text: strings.Repeat("- finding\n", 20),
		Subagents: []transcript.Subagent{{Name: "Explore", IsTeammate: true}}}
	short := long
	short.Text = "all good"
	m := liveWith([]transcript.Entry{long, short})
	v := tvOf(&m)
	if !v.entryExpandable(long) || v.entryExpandable(short) {
		t.Fatalf("expandable: long=%v short=%v, want true/false", v.entryExpandable(long), v.entryExpandable(short))
	}
	w := m.transcriptWidth()
	col := xansi.Strip(v.entryBlock(long, false, false, false, false, w))
	if !strings.Contains(col, "Explore") || !strings.Contains(col, "lines hidden") || strings.Count(col, "finding") >= 20 {
		t.Errorf("collapsed agent message:\n%s", col)
	}
	exp := xansi.Strip(v.entryBlock(long, true, false, false, false, w))
	if strings.Count(exp, "finding") != 20 || strings.Contains(exp, "lines hidden") {
		t.Errorf("expanded agent message:\n%s", exp)
	}
}
