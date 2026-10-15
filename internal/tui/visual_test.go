package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// captureClipboard records native clipboard writes for the test.
func captureClipboard(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := writeClipboard
	writeClipboard = func(s string) error { got = append(got, s); return nil }
	t.Cleanup(func() { writeClipboard = old })
	return &got
}

func runAll(m model, cmd tea.Cmd) model {
	for _, msg := range execCmd(cmd) {
		m, _ = upd(m, msg)
	}
	return m
}

func copyModel(entries ...transcript.Entry) model {
	m := withEntries(withView(testModel(), viewSession), entries)
	return withFocus(m, mainPane)
}

func TestCopyCardCopiesSource(t *testing.T) {
	got := captureClipboard(t)
	m := copyModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "**bold** and `code`"})
	m, cmd := upd(m, keyMsg("y"))
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "**bold** and `code`" {
		t.Fatalf("clipboard = %q, want the raw markdown", *got)
	}
	if m.flash != "copied" {
		t.Errorf("flash = %q, want copied", m.flash)
	}
}

func TestCopyCardTool(t *testing.T) {
	got := captureClipboard(t)
	m := copyModel(transcript.Entry{ID: "x1", Kind: transcript.EntryTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`, Result: "a\nb"})
	// A lone tool starts as a collapsed run summary; unfold it and step onto the card.
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, keyMsg("j"))
	m, cmd := upd(m, keyMsg("y"))
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "{\"command\":\"ls\"}\n\na\nb" {
		t.Fatalf("clipboard = %q", *got)
	}
}

func TestCopyCardWithoutSource(t *testing.T) {
	got := captureClipboard(t)
	m := copyModel(transcript.Entry{ID: "e1", Kind: transcript.EntryTurnEnd})
	m, cmd := upd(m, keyMsg("y"))
	runAll(m, cmd)
	if len(*got) != 0 || m.flash != "nothing to copy" {
		t.Errorf("clipboard = %q flash = %q, want nothing copied", *got, m.flash)
	}
}

func TestCopyEmitsOSC52(t *testing.T) {
	captureClipboard(t)
	m := copyModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "hi"})
	_, cmd := upd(m, keyMsg("y"))
	found := false
	for _, msg := range execCmd(cmd) {
		if strings.Contains(strings.ToLower(fmt.Sprintf("%T", msg)), "clipboard") {
			found = true
		}
	}
	if !found {
		t.Error("copy must also set the terminal clipboard (OSC52)")
	}
}

func TestMenuOnTranscriptCardOffersCopy(t *testing.T) {
	m := withMouse(copyModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "hi"}))
	x, y := itemCell(t, m, regMain, 0)
	m, _ = rightClick(m, x, y)
	wantMenu(t, m, "copy")
}

func TestDetailCopyCopiesItem(t *testing.T) {
	got := captureClipboard(t)
	m := detailTestModel(traceFixture("general", transcript.Entry{ID: "i1", Kind: transcript.EntryText, Text: "inner text"}))
	mm := *m.c.m
	mm = withFocus(mm, mainPane)
	mm, cmd := upd(mm, keyMsg("y"))
	runAll(mm, cmd)
	if len(*got) != 1 || (*got)[0] != "inner text" {
		t.Fatalf("clipboard = %q, want the detail item's source", *got)
	}
}

func TestEntrySourceKinds(t *testing.T) {
	m := tvIn(withView(testModel(), viewSession))
	teammate := transcript.Entry{Kind: transcript.EntrySubagent, Text: "ping",
		Subagents: []transcript.Subagent{{IsTeammate: true}}}
	for name, tc := range map[string]struct {
		e    transcript.Entry
		want string
	}{
		"shell":    {transcript.Entry{Kind: transcript.EntryShell, Text: "ls", Detail: "a\nb\n"}, "ls\n\na\nb"},
		"system":   {transcript.Entry{Kind: transcript.EntrySystem, Label: "note", Detail: "more"}, "note\n\nmore"},
		"teammate": {teammate, "ping"},
	} {
		got, ok, loading := m.entrySource(tc.e)
		if !ok || loading || got != tc.want {
			t.Errorf("%s: source = %q ok=%v loading=%v, want %q", name, got, ok, loading, tc.want)
		}
	}
}

func loadedToolEntry() transcript.Entry {
	return transcript.Entry{ID: "x1", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tool1",
		ToolInput: `{"command":"ls"}`, Result: "preview"}
}

// toolCopyModel runs `y` on a lone tool entry (unfolded, cursor on the card).
func toolCopyModel(c Client, e transcript.Entry) (model, tea.Cmd) {
	m := withFocus(withEntries(withView(streamModel(c), viewSession), []transcript.Entry{e}), mainPane)
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, keyMsg("j"))
	return upd(m, keyMsg("y"))
}

func TestCopyToolWhileBodyLoading(t *testing.T) {
	got := captureClipboard(t)
	rc := &recordingClient{}
	m, cmd := toolCopyModel(rc, loadedToolEntry())
	if m.flash != "copied (output still loading)" {
		t.Errorf("flash = %q", m.flash)
	}
	var detail bool
	for _, msg := range execCmd(cmd) {
		if _, ok := msg.(toolDetailMsg); ok {
			detail = true
		}
	}
	if !detail || countCalls(rc, api.MethodSessionToolDetail) != 1 {
		t.Errorf("copy must fetch the tool body: msg=%v calls=%d", detail, countCalls(rc, api.MethodSessionToolDetail))
	}
	if len(*got) != 1 || (*got)[0] != "{\"command\":\"ls\"}\n\npreview" {
		t.Errorf("clipboard = %q, want the preview", *got)
	}
}

func TestCopyToolAfterFailedFetch(t *testing.T) {
	got := captureClipboard(t)
	rc := &recordingClient{}
	e := loadedToolEntry()
	m := withFocus(withEntries(withView(streamModel(rc), viewSession), []transcript.Entry{e}), mainPane)
	m = withTr(m, func(t *transcriptComp) { t.toolBodies[e.ToolID] = toolBodyEntry{done: true} })
	m, _ = upd(m, keyMsg("l"))
	m, _ = upd(m, keyMsg("j"))
	m, cmd := upd(m, keyMsg("y"))
	runAll(m, cmd)
	if m.flash != "copied (output unavailable)" {
		t.Errorf("flash = %q", m.flash)
	}
	if len(*got) != 1 || (*got)[0] != "{\"command\":\"ls\"}\n\npreview" {
		t.Errorf("clipboard = %q, want the preview", *got)
	}
	if n := countCalls(rc, api.MethodSessionToolDetail); n != 0 {
		t.Errorf("a done body must not be refetched: calls = %d", n)
	}
}

func TestDetailCopyBodyFrame(t *testing.T) {
	got := captureClipboard(t)
	m := detailTestModel(transcript.Entry{ID: "b1", Kind: transcript.EntryUser, Text: "prompt"})
	f := m.topFrame()
	if f.items != nil {
		t.Fatal("fixture should open a body frame")
	}
	execCmd(m.actCopyCard(tea.KeyPressMsg{}))
	if len(*got) != 1 || (*got)[0] != "prompt" {
		t.Fatalf("clipboard = %q, want the entry's source", *got)
	}
}

func TestMenuCopyEntryCopies(t *testing.T) {
	got := captureClipboard(t)
	m := withMouse(copyModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "card text"}))
	x, y := itemCell(t, m, regMain, 0)
	m, _ = rightClick(m, x, y)
	m, cmd := upd(m, keyMsg("enter"))
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "card text" {
		t.Fatalf("clipboard = %q, want the card source", *got)
	}
}

func visualModel(entries ...transcript.Entry) model {
	if len(entries) == 0 {
		entries = []transcript.Entry{
			{ID: "u1", Kind: transcript.EntryUser, Text: "please list files"},
			{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"},
		}
	}
	m := copyModel(entries...)
	m.View()
	return m
}

func TestVisualEntersAndExits(t *testing.T) {
	m := visualModel()
	m = typeKeys(m, "v")
	if !trOf(m).visualActive() || m.screen() != "visual" {
		t.Fatalf("v: active=%v screen=%q, want visual", trOf(m).visualActive(), m.screen())
	}
	m = typeKeys(m, "v")
	if trOf(m).visualActive() {
		t.Error("v in charwise visual must exit")
	}
	m = typeKeys(m, "V")
	if s := trOf(m).transcript.visual; s == nil || !s.linewise {
		t.Fatal("V must enter linewise visual")
	}
	m = typeKeys(m, "v")
	if s := trOf(m).transcript.visual; s == nil || s.linewise {
		t.Error("v in linewise visual must switch to charwise")
	}
	m, _ = upd(m, keyMsg("esc"))
	if trOf(m).visualActive() {
		t.Error("esc must exit visual")
	}
	if viewOf(m) != viewSession {
		t.Error("esc in visual must not leave the transcript")
	}
}

func TestVisualCopyWord(t *testing.T) {
	got := captureClipboard(t)
	m := visualModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"})
	m = typeKeys(m, "vwe")
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha beta" {
		t.Fatalf("clipboard = %q, want %q", *got, "alpha beta")
	}
	if trOf(m).visualActive() {
		t.Error("y must exit visual")
	}
}

func TestVisualCopyWholeCardMatchesSource(t *testing.T) {
	got := captureClipboard(t)
	text := strings.Repeat("lorem ipsum dolor ", 12)
	m := visualModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: strings.TrimSpace(text)})
	m = typeKeys(m, "V")
	m = typeKeys(m, "G")
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != strings.TrimSpace(text) {
		t.Fatalf("clipboard = %q, want the paragraph rejoined without icon or indent", *got)
	}
}

func TestVisualHLMoveNotFold(t *testing.T) {
	m := visualModel()
	m = typeKeys(m, "v")
	before := trOf(m).transcript.expanded
	m = typeKeys(m, "lh")
	if !trOf(m).visualActive() {
		t.Fatal("h and l must stay in visual")
	}
	if len(trOf(m).transcript.expanded) != len(before) {
		t.Error("h and l must not fold in visual")
	}
}

// reversedCells reports, for each cell of the first screen line holding
// text, whether it is drawn in reverse video, starting at text's first cell.
func reversedCells(t *testing.T, screen, text string) []bool {
	t.Helper()
	for _, line := range strings.Split(screen, "\n") {
		plain := ansi.Strip(line)
		i := strings.Index(plain, text)
		if i < 0 {
			continue
		}
		return reversedAt(line, ansi.StringWidth(plain[:i]), len(text))
	}
	t.Fatalf("no line holds %q", text)
	return nil
}

func TestVisualPaintsSelection(t *testing.T) {
	m := visualModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"})
	if slices.Contains(reversedCells(t, m.View().Content, "alpha beta gamma"), true) {
		t.Fatal("nothing is reversed before visual mode")
	}
	m = typeKeys(m, "vw")
	// The selection runs from the a of alpha to the b of beta, inclusive.
	got := reversedCells(t, m.View().Content, "alpha beta gamma")
	for x, rev := range got {
		if want := x <= len("alpha "); rev != want {
			t.Errorf("cell %d (%q) reversed = %v, want %v", x, "alpha beta gamma"[x], rev, want)
		}
	}
}

func TestVisualSurvivesLiveAppend(t *testing.T) {
	got := captureClipboard(t)
	var entries []transcript.Entry
	for i := range 40 {
		entries = append(entries, transcript.Entry{ID: fmt.Sprintf("f%d", i), Kind: transcript.EntryText, Text: "filler"})
	}
	entries = append(entries, transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"})
	m := visualModel(entries...)
	m = typeKeys(m, "G") // pinned to the bottom
	m = typeKeys(m, "ve")
	scroll, cursor := trOf(m).transcript.scroll, trOf(m).transcript.cursor
	if scroll == 0 {
		t.Fatal("the transcript must overflow the view for this test")
	}
	es := append(slices.Clone(trOf(m).transcript.entries),
		transcript.Entry{ID: "t2", Kind: transcript.EntryText, Text: "new entry"},
		transcript.Entry{ID: "t3", Kind: transcript.EntryText, Text: "another entry"})
	m, _ = onTr(m, func(v tview) tea.Cmd {
		return v.updateMsg(transcriptMsg{id: v.sessionID, entries: es})
	})
	if !trOf(m).visualActive() {
		t.Fatal("an appended entry must not end visual mode")
	}
	if s, c := trOf(m).transcript.scroll, trOf(m).transcript.cursor; s != scroll || c != cursor {
		t.Errorf("scroll, cursor = %d, %d after the append, want %d, %d (no tail-follow in visual mode)", s, c, scroll, cursor)
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha" {
		t.Errorf("clipboard = %q, want the same text as before the append", *got)
	}
}

func TestVisualEndsWhenRowVanishes(t *testing.T) {
	m := visualModel()
	m = typeKeys(m, "Gv")
	m, _ = onTr(m, func(v tview) tea.Cmd {
		return v.updateMsg(transcriptMsg{id: v.sessionID, entries: v.transcript.entries[:1]})
	})
	if trOf(m).visualActive() {
		t.Error("visual must end when its row is gone")
	}
	if m.flash != "" {
		t.Errorf("flash = %q, want none", m.flash)
	}
}

func TestVisualSurvivesResize(t *testing.T) {
	got := captureClipboard(t)
	m := visualModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: strings.TrimSpace(strings.Repeat("word ", 60))})
	m = typeKeys(m, "vjjl")
	m, _ = upd(m, tea.WindowSizeMsg{Width: 40, Height: m.height})
	m.View()
	if !trOf(m).visualActive() {
		t.Fatal("a resize must not end visual mode")
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 {
		t.Fatalf("clipboard = %q, want one copy after the resize", *got)
	}
	// The cursor stays on the card's third line, second cell, of the rewrapped
	// card, so the copy is two full lines of words and the first two cells of
	// the third: "wo".
	text := (*got)[0]
	words := strings.Fields(strings.TrimSuffix(text, "wo"))
	if !strings.HasSuffix(text, " wo") || len(words) < 2 || strings.Count(text, "\n") != 0 {
		t.Errorf("clipboard = %q, want rejoined words ending in the third line's first two cells", text)
	}
	for _, w := range words {
		if w != "word" {
			t.Errorf("clipboard = %q holds %q, want only the card's words", text, w)
			break
		}
	}
	if per := len(words) / 2; len(words)%2 != 0 || per*len("word ") > 40 {
		t.Errorf("clipboard = %q, want two lines of the 40-wide layout", text)
	}
}

// noChrome fails when copied text holds the cursor gutter or an icon glyph
// (icons are private-use runes).
func noChrome(t *testing.T, text string) {
	t.Helper()
	for _, r := range text {
		if r == '▌' || unicode.In(r, unicode.Co) {
			t.Errorf("copy %q holds chrome rune %q", text, r)
		}
	}
}

func TestVisualCopyWholeUserPrompt(t *testing.T) {
	got := captureClipboard(t)
	m := visualModel(transcript.Entry{ID: "u1", Kind: transcript.EntryUser, Text: "please list files"})
	m = typeKeys(m, "VG")
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	// The band's header line is card text as displayed; the band's padding,
	// the gutter, and the band's inner pad are not.
	want := Icon.User.Glyph + " You\nplease list files"
	if len(*got) != 1 || (*got)[0] != want {
		t.Fatalf("clipboard = %q, want %q", *got, want)
	}
}

func TestVisualCopyWholeToolCard(t *testing.T) {
	got := captureClipboard(t)
	m := visualModel(transcript.Entry{ID: "x1", Kind: transcript.EntryTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`, Result: "a\nb"})
	// Unfold the run, step onto the tool, and expand it.
	m = typeKeys(m, "ljl")
	v := tvIn(m)
	lines, first := v.layoutEntries()
	r, ok := v.cursorRow()
	if !ok || r.isControl() {
		t.Fatal("the cursor must be on the tool card")
	}
	c := trOf(m).transcript.cursor
	end := len(lines)
	if c+1 < len(first) {
		end = first[c+1]
	}
	// V on the card's first line, then down to its last text line (the row's
	// final line is the blank separator).
	m = typeKeys(m, "V"+strings.Repeat("j", end-first[c]-2))
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 {
		t.Fatalf("clipboard = %q, want one copy", *got)
	}
	text := (*got)[0]
	noChrome(t, text)
	if !strings.HasPrefix(text, "Bash\n") || !strings.HasSuffix(text, "\na\nb") {
		t.Errorf("clipboard = %q, want the card's text from its header to the result", text)
	}
}

func TestVisualSurvivesExpandAbove(t *testing.T) {
	got := captureClipboard(t)
	m := visualModel(
		transcript.Entry{ID: "x1", Kind: transcript.EntryTool, ToolName: "Bash", ToolInput: `{"command":"ls"}`, Result: "a\nb"},
		transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"},
	)
	m = typeKeys(m, "G")
	m = typeKeys(m, "vwe")
	before := tvIn(m).displayRows()
	m, _ = onTr(m, func(v tview) tea.Cmd {
		for _, r := range v.displayRows() {
			if r.kind == runSummary {
				v.transcript.runs[r.run.key] = true
			}
		}
		return nil
	})
	m.View()
	if after := tvIn(m).displayRows(); len(after) <= len(before) {
		t.Fatalf("rows %d -> %d, want the run above to unfold", len(before), len(after))
	}
	if !trOf(m).visualActive() {
		t.Fatal("unfolding a card above must not end visual mode")
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha beta" {
		t.Errorf("clipboard = %q, want %q", *got, "alpha beta")
	}
}

func TestVisualNeedsText(t *testing.T) {
	m := copyModel()
	m = typeKeys(m, "v")
	if trOf(m).visualActive() || m.screen() != "transcript" {
		t.Errorf("v on an empty transcript: active=%v screen=%q", trOf(m).visualActive(), m.screen())
	}
}

func TestVisualFooterNamesTheMode(t *testing.T) {
	m := typeKeys(visualModel(), "v")
	if !strings.Contains(viewText(m), "VISUAL") {
		t.Error("the footer must name visual mode")
	}
	m = typeKeys(m, "V")
	if !strings.Contains(viewText(m), "VISUAL LINE") {
		t.Error("the footer must name linewise visual mode")
	}
}

func detailVisualModel(t *testing.T) model {
	t.Helper()
	v := detailTestModel(traceFixture("general",
		transcript.Entry{ID: "i1", Kind: transcript.EntryText, Text: "inner alpha beta"},
		transcript.Entry{ID: "i2", Kind: transcript.EntryText, Text: "second item"}))
	m := withFocus(*v.c.m, mainPane)
	m.View()
	return m
}

func TestDetailVisualCopy(t *testing.T) {
	got := captureClipboard(t)
	m := detailVisualModel(t)
	m = typeKeys(m, "ve")
	if m.screen() != "visual" {
		t.Fatalf("screen = %q, want visual", m.screen())
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "inner" {
		t.Fatalf("clipboard = %q, want %q", *got, "inner")
	}
}

func TestDetailVisualEscStaysInDetail(t *testing.T) {
	m := typeKeys(detailVisualModel(t), "V")
	m, _ = upd(m, keyMsg("esc"))
	if trOf(m).visualActive() {
		t.Fatal("esc must end visual")
	}
	if trOf(m).historyView != histDetail {
		t.Error("esc in visual must not pop the detail frame")
	}
}

func TestDetailVisualLinewiseAcrossItems(t *testing.T) {
	got := captureClipboard(t)
	m := detailVisualModel(t)
	m = typeKeys(m, "VG")
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || !strings.Contains((*got)[0], "inner alpha beta") || !strings.Contains((*got)[0], "second item") {
		t.Fatalf("clipboard = %q, want both items", *got)
	}
}

// followFrame is called directly on the top frame: it is what a streamed
// subagent frame runs when its entries grow.
func TestDetailVisualHoldsOnFollowFrame(t *testing.T) {
	got := captureClipboard(t)
	var items []transcript.Entry
	for i := range 40 {
		items = append(items, transcript.Entry{ID: fmt.Sprintf("f%d", i), Kind: transcript.EntryText, Text: "filler"})
	}
	items = append(items, transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"})
	v := detailTestModel(traceFixture("general", items...))
	m := withFocus(*v.c.m, mainPane)
	m.View()
	m = typeKeys(m, "G") // pinned to the bottom
	m = typeKeys(m, "ve")
	f := trOf(m).transcript.detailStack[len(trOf(m).transcript.detailStack)-1]
	scroll, cursor := f.scroll, f.cursor
	if scroll == 0 {
		t.Fatal("the frame must overflow the view for this test")
	}
	es := append(slices.Clone(f.items),
		transcript.Entry{ID: "t2", Kind: transcript.EntryText, Text: "new entry"},
		transcript.Entry{ID: "t3", Kind: transcript.EntryText, Text: "another entry"})
	m, _ = onTr(m, func(v tview) tea.Cmd {
		v.followFrame(v.topFrame(), es)
		return nil
	})
	if !trOf(m).visualActive() {
		t.Fatal("new entries must not end visual mode")
	}
	f = trOf(m).transcript.detailStack[len(trOf(m).transcript.detailStack)-1]
	if f.scroll != scroll || f.cursor != cursor {
		t.Errorf("scroll, cursor = %d, %d after follow, want %d, %d (no tail-follow in visual mode)", f.scroll, f.cursor, scroll, cursor)
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha" {
		t.Errorf("clipboard = %q, want the same text as before the follow", *got)
	}
}

// A selection in a detail frame is not the stream's: stream entries arriving
// under it still tail-follow.
func TestDetailVisualKeepsStreamPinned(t *testing.T) {
	var entries []transcript.Entry
	for i := range 40 {
		entries = append(entries, transcript.Entry{ID: fmt.Sprintf("f%d", i), Kind: transcript.EntryText, Text: "filler"})
	}
	entries = append(entries, transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"})
	m := visualModel(entries...)
	m = typeKeys(m, "G")
	if tvIn(m).transcript.scroll == 0 || tvIn(m).transcript.scroll != tvIn(m).maxScroll() {
		t.Fatal("the transcript must overflow and be pinned for this test")
	}
	m, _ = upd(m, keyMsg("enter"))
	if trOf(m).historyView != histDetail {
		t.Fatal("enter must open the detail view")
	}
	m = typeKeys(m, "V")
	if !trOf(m).visualActive() {
		t.Fatal("V must start visual in the detail view")
	}
	appendStream := func(m model, ids ...string) model {
		es := slices.Clone(trOf(m).transcript.entries)
		for _, id := range ids {
			es = append(es, transcript.Entry{ID: id, Kind: transcript.EntryText, Text: "new " + id})
		}
		m, _ = onTr(m, func(v tview) tea.Cmd {
			return v.updateMsg(transcriptMsg{id: v.sessionID, entries: es})
		})
		return m
	}
	m = appendStream(m, "n1", "n2")
	m, _ = upd(m, keyMsg("esc"))
	m, _ = upd(m, keyMsg("esc"))
	if trOf(m).historyView != histTranscript {
		t.Fatal("esc twice must return to the stream")
	}
	m.View()
	if s, ms := tvIn(m).transcript.scroll, tvIn(m).maxScroll(); s != ms {
		t.Fatalf("stream scroll = %d, want pinned at %d", s, ms)
	}
	m = appendStream(m, "n3", "n4")
	if s, ms := tvIn(m).transcript.scroll, tvIn(m).maxScroll(); s != ms {
		t.Errorf("after a further append: stream scroll = %d, want pinned at %d", s, ms)
	}
}

func TestDetailVisualBodyFrame(t *testing.T) {
	got := captureClipboard(t)
	text := strings.Repeat("lorem ipsum dolor sit amet ", 12) + "tail"
	v := detailTestModel(transcript.Entry{ID: "u1", Kind: transcript.EntryUser, Text: text})
	if v.topFrame().items != nil {
		t.Fatal("fixture should open a body frame")
	}
	m := withFocus(*v.c.m, mainPane)
	m.View()
	m = typeKeys(m, "ve")
	if m.screen() != "visual" {
		t.Fatalf("screen = %q, want visual", m.screen())
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "You" { // the body's heading, from its first column
		t.Fatalf("charwise clipboard = %q, want %q", *got, "You")
	}
	m = typeKeys(m, "VGojj") // the text lines, below the heading and its blank line
	m, cmd = typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 2 {
		t.Fatalf("clipboard = %q, want a second copy", *got)
	}
	c := (*got)[1]
	if c != text {
		t.Errorf("linewise clipboard = %q, want the prompt with its wraps rejoined", c)
	}
}

func textCell(t *testing.T, m model, i, col int) (x, y int) {
	t.Helper()
	x, y = itemCell(t, m, regMain, i)
	return x - 1 + m.transcriptTextX() + textInset + col, y
}

func mouseText() model {
	return withMouse(visualModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"}))
}

func TestDragSelectsWithoutCopy(t *testing.T) {
	got := captureClipboard(t)
	m := mouseText()
	x, y := textCell(t, m, 0, 0)
	m = dragXY(m, x, y, x+4, y)
	if !trOf(m).visualActive() {
		t.Fatal("a drag must start visual mode")
	}
	if len(*got) != 0 {
		t.Fatalf("a drag must not copy, got %q", *got)
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha" {
		t.Errorf("clipboard = %q, want alpha", *got)
	}
}

func TestDragReleaseAppliesFinalPoint(t *testing.T) {
	got := captureClipboard(t)
	m := mouseText()
	x, y := textCell(t, m, 0, 0)
	m, _ = pressAt(m, x, y)
	m, _ = upd(m, tea.MouseMotionMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
	m, _ = upd(m, tea.MouseReleaseMsg{X: x + 9, Y: y, Button: tea.MouseLeft}) // motion to here was throttled away
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha beta" {
		t.Errorf("clipboard = %q, want %q", *got, "alpha beta")
	}
}

func TestClickWithoutDragStillSelectsCard(t *testing.T) {
	m := withMouse(visualModel())
	x, y := itemCell(t, m, regMain, 1)
	m, _ = click(m, x, y)
	if trOf(m).transcript.cursor != 1 || trOf(m).visualActive() {
		t.Errorf("cursor=%d visual=%v, want the card selected and no visual mode", trOf(m).transcript.cursor, trOf(m).visualActive())
	}
	m, _ = click(m, x, y)
	if trOf(m).historyView != histDetail {
		t.Error("a click on the selected card must still open it")
	}
}

func TestPressThenDragOnSelectedCardDoesNotOpen(t *testing.T) {
	m := mouseText()
	x, y := textCell(t, m, 0, 0)
	m = dragXY(m, x, y, x+4, y)
	if trOf(m).historyView == histDetail {
		t.Error("a drag on the selected card must not open it")
	}
}

func TestRightClickInVisualKeepsSelection(t *testing.T) {
	m := mouseText()
	x, y := textCell(t, m, 0, 0)
	m = dragXY(m, x, y, x+4, y)
	before := *trOf(m).transcript.visual
	m, _ = rightClick(m, x+8, y)
	wantMenu(t, m, "copy", "back")
	if s := trOf(m).transcript.visual; s == nil || *s != before {
		t.Error("a right click must leave the selection as it was")
	}
}

func TestLeftClickInVisualExits(t *testing.T) {
	m := mouseText()
	x, y := textCell(t, m, 0, 0)
	m = dragXY(m, x, y, x+4, y)
	m, _ = click(m, x, y)
	if trOf(m).visualActive() {
		t.Error("a click must end visual mode")
	}
}

func TestDragReleasedOutsideClamps(t *testing.T) {
	got := captureClipboard(t)
	m := mouseText()
	x, y := textCell(t, m, 0, 0)
	m = dragXY(m, x, y, 0, m.height-1) // released over the footer, left edge
	if !trOf(m).visualActive() {
		t.Fatal("the selection must survive a release outside the pane")
	}
	if viewOf(m) != viewSession || trOf(m).historyView != histTranscript {
		t.Error("a release outside must not click anything")
	}
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "alpha beta gamma" {
		t.Errorf("clipboard = %q, want %q", *got, "alpha beta gamma")
	}
}

func TestDetailDragSelects(t *testing.T) {
	got := captureClipboard(t)
	m := withMouse(detailVisualModel(t))
	r, c := findMainBlock(t, m, "inner alpha")
	m = dragXY(m, c, r, c+4, r)
	m, cmd := typeKeysCmd(m, "y")
	runAll(m, cmd)
	if len(*got) != 1 || (*got)[0] != "inner" {
		t.Errorf("clipboard = %q, want inner", *got)
	}
}

// noSelection reports whether no frame and not the stream holds a selection.
func noSelection(m model) bool {
	tr := trOf(m)
	if tr.transcript.visual != nil {
		return false
	}
	for _, f := range tr.transcript.detailStack {
		if f.visual != nil {
			return false
		}
	}
	return true
}

func TestCloseClickInVisualLeavesNoSelection(t *testing.T) {
	m := nestedDetail(true)
	m = typeKeys(m, "v")
	if !trOf(m).visualActive() {
		t.Fatal("v must start visual mode")
	}
	x, y := closeCell(t, m)
	m, _ = click(m, x, y)
	if n := len(trOf(m).transcript.detailStack); n != 1 {
		t.Fatalf("detail depth = %d, want 1", n)
	}
	if trOf(m).visualActive() || !noSelection(m) {
		t.Error("a close click must leave no selection anywhere")
	}
}

func TestFoldClickInVisualExits(t *testing.T) {
	m := withMouse(runModel(false))
	m = typeKeys(m, "v")
	if !trOf(m).visualActive() {
		t.Fatal("v must start visual mode")
	}
	m.View()
	p, found := foldZoneCells(m)[1]
	if !found {
		t.Fatal("no fold zone on the summary row")
	}
	x, y := p[0], p[1]
	m, _ = click(m, x, y)
	if trOf(m).visualActive() || !noSelection(m) {
		t.Error("a fold click must end visual mode")
	}
}

func TestDrillClickInVisualLeavesNoSelection(t *testing.T) {
	m := withMouse(visualModel())
	m = typeKeys(m, "v")
	x, y := itemCell(t, m, regMain, trOf(m).transcript.cursor)
	m, _ = click(m, x, y)
	if trOf(m).historyView != histDetail {
		t.Fatal("a click on the selected card must open it")
	}
	if trOf(m).visualActive() || !noSelection(m) {
		t.Error("a click that opens a card must leave no selection anywhere")
	}
}

func manyTexts(n int) []transcript.Entry {
	es := make([]transcript.Entry, n)
	for i := range es {
		es[i] = transcript.Entry{ID: fmt.Sprintf("t%d", i), Kind: transcript.EntryText, Text: fmt.Sprintf("line %d", i)}
	}
	return es
}

func mainCenter(m model) (x, y int) {
	r := m.mainRect()
	return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2
}

func TestWheelInStreamVisualScrolls(t *testing.T) {
	m := withMouse(visualModel(manyTexts(40)...))
	m = typeKeys(m, "v")
	if !trOf(m).visualActive() {
		t.Fatal("v must start visual mode")
	}
	before, scroll := *trOf(m).transcript.visual, trOf(m).transcript.scroll
	x, y := mainCenter(m)
	m, _ = wheelAt(m, x, y, 20)
	if trOf(m).transcript.scroll == scroll {
		t.Error("the wheel must scroll in visual mode")
	}
	if s := trOf(m).transcript.visual; s == nil || *s != before {
		t.Error("the wheel must leave the selection as it was")
	}
}

func TestWheelInDetailVisualScrolls(t *testing.T) {
	v := detailTestModel(traceFixture("general", manyTexts(40)...))
	m := withMouse(withFocus(*v.c.m, mainPane))
	m.View()
	m = typeKeys(m, "v")
	if !trOf(m).visualActive() {
		t.Fatal("v must start visual mode")
	}
	top := func(m model) detailFrame {
		st := trOf(m).transcript.detailStack
		return st[len(st)-1]
	}
	before, scroll := *top(m).visual, top(m).scroll
	x, y := mainCenter(m)
	m, _ = wheelAt(m, x, y, 10)
	if top(m).scroll == scroll {
		t.Error("the wheel must scroll the detail in visual mode")
	}
	if s := top(m).visual; s == nil || *s != before {
		t.Error("the wheel must leave the selection as it was")
	}
}

func TestMenuOffersNoCopyWithoutSource(t *testing.T) {
	m := withMouse(copyModel(
		transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "hi"},
		transcript.Entry{ID: "e1", Kind: transcript.EntryTurnEnd, ModelName: "Opus 4.8", DurationMs: 62000},
	))
	x, y := itemCell(t, m, regMain, 1)
	m, _ = rightClick(m, x, y)
	if p, ok := m.popups.front().(contextMenu); ok {
		t.Errorf("menu on a turn end = %v, want none", menuNames(p))
	}
	m.popups = nil
	x, y = itemCell(t, m, regMain, 0)
	m, _ = rightClick(m, x, y)
	wantMenu(t, m, "copy")
}

func TestMenuOffersNoCopyOnRunSummary(t *testing.T) {
	m := withMouse(runModel(false))
	x, y := itemCell(t, m, regMain, 1) // the run's summary row
	m, _ = rightClick(m, x, y)
	if p, ok := m.popups.front().(contextMenu); ok {
		t.Errorf("menu on a run summary = %v, want none", menuNames(p))
	}
}

func TestMenuOnBodyFrameOffersCopy(t *testing.T) {
	v := detailTestModel(transcript.Entry{ID: "u1", Kind: transcript.EntryUser, Text: "prompt"})
	m := withMouse(withFocus(*v.c.m, mainPane))
	if got := m.applicable(trOf(m).menu(&ctx{m: &m})); len(got) != 1 || got[0].name != detailKeys.Copy.name {
		t.Errorf("body frame menu = %v, want copy", got)
	}
}

func TestClickInAnotherPaneEndsVisual(t *testing.T) {
	m := mouseText()
	m.left.hidden, m.width = false, max(m.width, sidebarMinWidth+40)
	x, y := textCell(t, m, 0, 0)
	m = dragXY(m, x, y, x+4, y)
	if !trOf(m).visualActive() {
		t.Fatal("a drag must start visual mode")
	}
	m.View()
	var tree *hitArea
	for _, a := range m.hits.areas {
		if a.region == regTree {
			tree = a
		}
	}
	if tree == nil {
		t.Fatal("no left sidebar on screen")
	}
	m, _ = click(m, tree.rect.Min.X+1, tree.rect.Max.Y-1)
	if m.focused != leftSidebar {
		t.Errorf("focused = %v, want the click to focus the sidebar", m.focused)
	}
	if trOf(m).transcript.visual != nil {
		t.Error("a click in another pane must end the transcript's visual mode")
	}
}

func TestFoldCommandInVisualEndsIt(t *testing.T) {
	m := runModel(true)
	m = withTr(m, func(t *transcriptComp) {
		v := t.bind(&ctx{m: &m})
		t.transcript.cursor = rowIndexOf(t.transcript.entries, v.displayRows(), rowRef{id: "x1"})
	})
	m = typeKeys(m, "v")
	if !trOf(m).visualActive() {
		t.Fatal("v must start visual mode")
	}
	m, _ = upd(m, cmdMsg(projectsKeys.ToggleVerbose.name))
	if trOf(m).visualActive() || !noSelection(m) {
		t.Error("folding the selection's row away must end visual mode")
	}
}

func TestVisualShowsPendingKeyHint(t *testing.T) {
	m := typeKeys(visualModel(), "vg")
	if got, want := m.currentFooter(), asstStyle.Render(m.keyHint()); got != want {
		t.Errorf("footer = %q, want the pending key hint %q", ansi.Strip(got), ansi.Strip(want))
	}
	m = typeKeys(m, "g")
	if !strings.Contains(ansi.Strip(m.currentFooter()), "VISUAL") {
		t.Error("after the sequence completes the footer must name visual mode again")
	}
}

func TestDetailVisualRegistersOneCloseZone(t *testing.T) {
	m := nestedDetail(true)
	m = typeKeys(m, "v")
	m.View()
	n := 0
	for _, a := range m.hits.areas {
		for _, z := range a.zones {
			if z.target.kind == hitClose {
				n++
			}
		}
	}
	if n != 1 {
		t.Errorf("close zones = %d, want 1", n)
	}
}

// cursorChar is the character under the terminal cursor of the last frame.
func cursorChar(t *testing.T, m model) string {
	t.Helper()
	v := m.View()
	if v.Cursor == nil {
		t.Fatal("no terminal cursor")
	}
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	return ansi.Cut(lines[v.Cursor.Y], v.Cursor.X, v.Cursor.X+1)
}

func TestVisualTerminalCursorMarksCursorEnd(t *testing.T) {
	m := visualModel(transcript.Entry{ID: "t1", Kind: transcript.EntryText, Text: "alpha beta gamma"})
	m.hits = &hitMap{}
	if m.View().Cursor != nil {
		t.Fatal("no terminal cursor outside visual mode")
	}
	m = typeKeys(m, "v")
	if got := cursorChar(t, m); got != "a" {
		t.Errorf("cursor on %q, want the a of alpha", got)
	}
	m = typeKeys(m, "w")
	if got := cursorChar(t, m); got != "b" {
		t.Errorf("after w, cursor on %q, want the b of beta", got)
	}
	m = typeKeys(m, "o")
	if got := cursorChar(t, m); got != "a" {
		t.Errorf("after o, cursor on %q, want the anchor's a", got)
	}
	m, _ = upd(m, keyMsg("esc"))
	if m.View().Cursor != nil {
		t.Error("the terminal cursor must go when visual mode ends")
	}
}

func TestDetailVisualTerminalCursor(t *testing.T) {
	m := detailVisualModel(t)
	m.hits = &hitMap{}
	m = typeKeys(m, "vw")
	if got := cursorChar(t, m); got != "a" {
		t.Errorf("cursor on %q, want the a of alpha", got)
	}
}
