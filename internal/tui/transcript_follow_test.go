package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// userEntries builds n short user entries — enough to overflow a small viewport.
func userEntries(n int) []transcript.Entry {
	out := make([]transcript.Entry, n)
	for i := range out {
		out[i] = transcript.Entry{ID: fmt.Sprintf("c%d", i), Kind: transcript.EntryUser, Text: fmt.Sprintf("message %d", i)}
	}
	return out
}

// Opening a session pins the transcript to the bottom (newest content), not the top.
func TestOpenSessionStartsAtBottom(t *testing.T) {
	m := testModel()
	m.width, m.height = 80, 10
	sid := "s1"
	m.order = []string{sid}
	m.sessions = map[string]session.Session{sid: {ID: sid}}
	m.transcriptCache = map[string]cachedTranscript{sid: {entries: userEntries(20)}}

	res, _ := m.baseKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = res.(model)

	if tvOf(&m).maxScroll() == 0 {
		t.Fatal("setup: cached entries should overflow the viewport")
	}
	if trOf(m).transcript.scroll != tvOf(&m).maxScroll() {
		t.Errorf("open should pin to bottom: scroll=%d, maxScroll=%d", trOf(m).transcript.scroll, tvOf(&m).maxScroll())
	}
}

// deltaModel returns a live transcript holding entries, with an active parent
// subscription matching subID "x".
func deltaModel() model {
	m := testModel()
	m.width, m.height = 80, 10
	m = withLive(m, "s1")
	m = withTr(m, func(t *transcriptComp) { t.activeSub = subRef{subID: "x", sessionID: "s1"} })
	m.transcriptCache = map[string]cachedTranscript{}
	m = withTr(m, func(t *transcriptComp) { t.transcript.entries = userEntries(20) })
	return m
}

func appendDelta() transcriptDeltaMsg {
	return transcriptDeltaMsg{
		ref:   subRef{subID: "x", sessionID: "s1"},
		delta: api.TranscriptDelta{FromIndex: 20, Entries: userEntries(1)},
	}
}

// A delta arriving while pinned to the bottom keeps following the newest content.
func TestDeltaFollowsWhenAtBottom(t *testing.T) {
	m := deltaModel()
	m = withTr(m, func(t *transcriptComp) { t.transcript.scroll = tvOf(&m).maxScroll() }) // at bottom

	res, _ := m.Update(appendDelta())
	m = res.(model)

	if got := len(trOf(m).transcript.entries); got != 21 {
		t.Fatalf("delta should append a chunk, len=%d", got)
	}
	if trOf(m).transcript.scroll != tvOf(&m).maxScroll() {
		t.Errorf("should tail to bottom: scroll=%d, maxScroll=%d", trOf(m).transcript.scroll, tvOf(&m).maxScroll())
	}
}

// A delta arriving while scrolled up must not yank the viewport to the bottom.
func TestDeltaDoesNotFollowWhenScrolledUp(t *testing.T) {
	m := deltaModel()
	m = withTr(m, func(t *transcriptComp) { t.transcript.scroll = 0 }) // scrolled to top
	if tvOf(&m).maxScroll() == 0 {
		t.Fatal("setup: entries should overflow so 'scrolled up' is meaningful")
	}

	res, _ := m.Update(appendDelta())
	m = res.(model)

	if trOf(m).transcript.scroll != 0 {
		t.Errorf("scrolled-up view should stay put, scroll=%d", trOf(m).transcript.scroll)
	}
}

func newEntryDelta() transcriptDeltaMsg {
	return transcriptDeltaMsg{
		ref: subRef{subID: "x", sessionID: "s1"},
		delta: api.TranscriptDelta{FromIndex: 20, Entries: []transcript.Entry{
			{ID: "new", Kind: transcript.EntryUser, Text: "newest"}}},
	}
}

// Following with the cursor on the last entry keeps the cursor on the newest entry.
func TestDeltaMovesCursorToNewLastWhenFollowing(t *testing.T) {
	m := deltaModel()
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.cursor = 19
		t.transcript.scroll = tvOf(&m).maxScroll()
	})
	res, _ := m.Update(newEntryDelta())
	m = res.(model)
	if got := trOf(m).transcript.cursor; got != 20 {
		t.Errorf("cursor = %d, want 20 (the new last entry)", got)
	}
}

// Following with the cursor on an earlier entry leaves it there.
func TestDeltaKeepsEarlierCursorWhenFollowing(t *testing.T) {
	m := deltaModel()
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.cursor = 18
		t.transcript.scroll = tvOf(&m).maxScroll()
	})
	res, _ := m.Update(newEntryDelta())
	m = res.(model)
	if got := trOf(m).transcript.cursor; got != 18 {
		t.Errorf("cursor = %d, want 18", got)
	}
}

// A cursor on the last entry doesn't follow when the view isn't at the bottom.
func TestDeltaKeepsCursorOnLastWhenScrolledUp(t *testing.T) {
	m := deltaModel()
	m = withTr(m, func(t *transcriptComp) {
		t.transcript.cursor = 19
		t.transcript.scroll = 0
	})
	res, _ := m.Update(newEntryDelta())
	m = res.(model)
	tr := trOf(m)
	if tr.transcript.cursor != 19 || tr.transcript.scroll != 0 {
		t.Errorf("cursor=%d scroll=%d, want 19 and 0 (not following)", tr.transcript.cursor, tr.transcript.scroll)
	}
}

// The same rule holds inside a streamed subagent drill-down frame.
func TestSubagentFrameFollowsWhenCursorOnLast(t *testing.T) {
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
	deliver := func(from int, es []transcript.Entry) {
		d := sub
		d.delta.SubID, d.delta.FromIndex, d.delta.Entries = d.ref.subID, from, es
		m, _ = upd(m, d)
	}
	var first []transcript.Entry
	for i := range 30 {
		first = append(first, transcript.Entry{ID: fmt.Sprint(i), Kind: transcript.EntryText, Text: fmt.Sprint("step ", i)})
	}
	deliver(0, first)
	f := trOf(m).transcript.detailStack[0]
	if f.cursor != 29 {
		t.Fatalf("first load: frame cursor = %d, want 29 (tail the trace)", f.cursor)
	}
	deliver(30, []transcript.Entry{{ID: "30", Kind: transcript.EntryText, Text: "step 30"}})
	tv := tvOf(&m)
	f = trOf(m).transcript.detailStack[0]
	if f.cursor != 30 {
		t.Errorf("frame cursor = %d, want 30 (the new last entry)", f.cursor)
	}
	if max := tv.frameMaxScroll(&f); f.scroll != max {
		t.Errorf("frame scroll = %d, want %d (pinned to the bottom)", f.scroll, max)
	}
	if !strings.Contains(viewText(m), "step 30") {
		t.Errorf("newest step not on screen:\n%s", viewText(m))
	}
	// On the last entry but scrolled up: no follow.
	m = withTr(m, func(t *transcriptComp) { t.transcript.detailStack[0].scroll = 0 })
	deliver(31, []transcript.Entry{{ID: "31", Kind: transcript.EntryText, Text: "step 31"}})
	if f := trOf(m).transcript.detailStack[0]; f.cursor != 30 || f.scroll != 0 {
		t.Errorf("scrolled up: cursor=%d scroll=%d, want 30 and 0 (not following)", f.cursor, f.scroll)
	}
	// At the bottom but off the last entry: the view tails, the cursor stays.
	m = withTr(m, func(t *transcriptComp) {
		f := &t.transcript.detailStack[0]
		f.cursor = 5
	})
	m = withTr(m, func(t *transcriptComp) {
		f := &t.transcript.detailStack[0]
		f.scroll = tvOver(&m, *t).frameMaxScroll(f)
	})
	deliver(32, []transcript.Entry{{ID: "32", Kind: transcript.EntryText, Text: "step 32"}})
	f = trOf(m).transcript.detailStack[0]
	if f.cursor != 5 {
		t.Errorf("cursor off the last entry moved to %d, want 5", f.cursor)
	}
	if max := tvOf(&m).frameMaxScroll(&f); f.scroll != max {
		t.Errorf("frame at the bottom: scroll = %d, want %d (still tailing)", f.scroll, max)
	}
}
