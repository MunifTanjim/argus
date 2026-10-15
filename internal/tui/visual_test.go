package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
