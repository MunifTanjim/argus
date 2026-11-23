package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

var threadT0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func threadEntries() []transcript.Entry {
	ts := func(m int) string { return threadT0.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	return []transcript.Entry{
		{ID: "u1", Kind: transcript.EntryUser, Text: "remember the flat search", Timestamp: ts(0)},
		{ID: "a1", Kind: transcript.EntryText, Text: "noted", Timestamp: ts(1)},
		{ID: "ev:7", Kind: transcript.EntryArgus, Label: "memory.changed", Detail: "Memory: added flat search", Timestamp: ts(2),
			Event: &transcript.ArgusEvent{ID: 7, Kind: "memory.changed", Payload: json.RawMessage(`{"commit":"abc123","summary":"added flat search"}`)}},
		{ID: "a2", Kind: transcript.EntryText, Text: "done", Timestamp: ts(3)},
	}
}

func threadClient() *botClient {
	cl := botViewClient()
	cl.timeline = api.BotTimeline{ThreadID: "n1:t1", Entries: threadEntries(),
		Sessions: []api.BotTimelineSession{{SessionID: "n1:s1", Live: true}}}
	return cl
}

// openedThread opens thread t1 from unready's bot view and loads it.
func openedThread(t *testing.T, cl *botClient) model {
	t.Helper()
	m := loadedBotView(t, cl)
	m, cmd := upd(m, keyMsg("enter"))
	if _, ok := m.main.top().(threadComp); !ok || cmd == nil {
		t.Fatalf("enter opens the thread: %T", m.main.top())
	}
	m, _ = upd(m, m.loadTimelineCmd("n1:t1")())
	return m
}

func threadCursorID(m model) string {
	th := m.main.top().(threadComp)
	c := &ctx{m: &m}
	ref, _ := th.bind(c).currentRowID()
	return ref.id
}

func TestThreadViewRendersArgusEvents(t *testing.T) {
	m := openedThread(t, threadClient())
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"flat search", "Argus", "Memory: added flat search", "u undo", "noted"} {
		if !strings.Contains(out, want) {
			t.Errorf("thread view lacks %q:\n%s", want, out)
		}
	}
}

func TestThreadViewKeepsCursorOnRefresh(t *testing.T) {
	cl := threadClient()
	m := openedThread(t, cl)
	m = typeKeys(m, "k")
	if id := threadCursorID(m); id != "ev:7" {
		t.Fatalf("k selects the event, got %q", id)
	}
	es := append([]transcript.Entry{{ID: "ev:1", Kind: transcript.EntryArgus, Detail: "Session started",
		Timestamp: threadT0.Add(-time.Minute).Format(time.RFC3339Nano)}}, threadEntries()...)
	cl.timeline.Entries = append(es, transcript.Entry{ID: "a3", Kind: transcript.EntryText, Text: "more",
		Timestamp: threadT0.Add(5 * time.Minute).Format(time.RFC3339Nano)})
	m, _ = upd(m, m.applyEvent(api.Notification{Method: api.MethodBotChanged})())
	m, _ = upd(m, m.loadTimelineCmd("n1:t1")())
	if id := threadCursorID(m); id != "ev:7" {
		t.Fatalf("refresh moved the cursor to %q", id)
	}
}

func TestThreadViewMarksRead(t *testing.T) {
	cl := threadClient()
	m := openedThread(t, cl)
	markReads := func() []api.BotMarkReadParams {
		var got []api.BotMarkReadParams
		for i, call := range cl.calls {
			if call == api.MethodBotThreadMarkRead {
				got = append(got, cl.params[i].(api.BotMarkReadParams))
			}
		}
		return got
	}
	// The thread's last activity (a turn's end) comes after its last entry:
	// marking at the entry would leave it unread.
	active := threadT0.Add(4*time.Minute + 123)
	m.botThreads["n1:u"][0].LastActiveAt = active
	m2, cmd := m.Update(m.loadTimelineCmd("n1:t1")())
	m = m2.(model)
	if cmd == nil {
		t.Fatal("a shown unread thread is marked read")
	}
	runCmd(cmd)
	want := api.BotMarkReadParams{ThreadID: "n1:t1", Position: active.UnixNano()}
	if got := markReads(); len(got) != 1 || got[0] != want {
		t.Fatalf("markRead = %+v, want %+v", got, want)
	}

	// A read thread is not marked again.
	cl.calls, cl.params = nil, nil
	m.botThreads["n1:u"][0].Unread = false
	m2, cmd = m.Update(m.loadTimelineCmd("n1:t1")())
	m = m2.(model)
	runCmd(cmd)
	if got := markReads(); len(got) != 0 {
		t.Errorf("a read thread was marked again: %+v", got)
	}

	// A thread under another view is not marked when its activity changes.
	m.main = m.main.push(botComp{botID: "n1:u"})
	threads := botViewThreads()
	threads[0].LastActiveAt = active.Add(time.Minute)
	m2, cmd = m.Update(botsMsg{names: map[string]string{}, bots: m.bots,
		threads: map[string][]api.BotThreadInfo{"n1:u": threads}})
	m = m2.(model)
	runCmd(cmd)
	if got := markReads(); len(got) != 0 {
		t.Errorf("a covered thread was marked read: %+v", got)
	}
}

func TestThreadViewUndoMemoryEvent(t *testing.T) {
	cl := threadClient()
	m := openedThread(t, cl)
	m, cmd := upd(m, keyMsg("u"))
	if cmd != nil || m.flash != "not a change to undo" {
		t.Fatalf("u off a memory event: flash %q", m.flash)
	}
	m = typeKeys(m, "k")
	m, cmd = upd(m, keyMsg("u"))
	if cmd != nil || !m.main.top().raw(&ctx{m: &m}) {
		t.Fatal("u on the memory event asks to confirm")
	}
	if m, cmd = upd(m, keyMsg("n")); cmd != nil || m.main.top().raw(&ctx{m: &m}) {
		t.Fatal("n cancels the undo")
	}
	m, _ = upd(m, keyMsg("u"))
	m, cmd = upd(m, keyMsg("y"))
	if cmd == nil {
		t.Fatal("y undoes")
	}
	cl.undoErr = errors.New("conflicts with a later change")
	m, _ = upd(m, cmd())
	p, n := botParams[api.BotMemoryUndoParams](cl, api.MethodBotMemoryUndo)
	if n != 1 || p != (api.BotMemoryUndoParams{BotID: "n1:u", Commit: "abc123", ThreadID: "n1:t1"}) {
		t.Fatalf("undo params = %+v (%d calls)", p, n)
	}
	if !strings.Contains(m.flash, "conflicts with a later change") {
		t.Errorf("flash = %q", m.flash)
	}
	m, _ = upd(m, threadActionMsg{threadID: "n1:t1", verb: "undo"})
	if m.flash != "undone" {
		t.Errorf("flash after an undo = %q", m.flash)
	}
}

func TestThreadViewHidesUndoneChange(t *testing.T) {
	cl := threadClient()
	cl.timeline.Entries = append(cl.timeline.Entries, transcript.Entry{ID: "ev:8", Kind: transcript.EntryArgus, Label: "memory.undone",
		Detail: "Undid a change", Timestamp: threadT0.Add(4 * time.Minute).Format(time.RFC3339Nano),
		Event: &transcript.ArgusEvent{ID: 8, Kind: "memory.undone", Payload: json.RawMessage(`{"commit":"abc123","revert":"def456"}`)}})
	m := openedThread(t, cl)
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "u undo") {
		t.Errorf("an undone change still offers undo:\n%s", out)
	}
	m = typeKeys(m, "kk")
	if id := threadCursorID(m); id != "ev:7" {
		t.Fatalf("cursor on %q", id)
	}
	m, cmd := upd(m, keyMsg("u"))
	if cmd != nil || m.flash != "change already undone" || m.main.top().raw(&ctx{m: &m}) {
		t.Fatalf("u on an undone change: flash %q", m.flash)
	}
}

func TestThreadViewOpenSessionAndReply(t *testing.T) {
	cl := threadClient()
	m := openedThread(t, cl)
	c := &ctx{m: &m}
	m.main.top().handleKey(c, keyMsg("o"))
	if len(c.actions) == 0 || c.actions[len(c.actions)-1].kind != actOpenSession || c.actions[len(c.actions)-1].id != "n1:s1" {
		t.Fatalf("o opens the live session: %v", actionKinds(c))
	}
	m = pressKeys(m, keyMsg("tab"))
	m = typeKeys(m, "go on")
	m, cmd := upd(m, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter sends the reply")
	}
	cmd()
	var p api.BotSendParams
	for i, call := range cl.calls {
		if call == api.MethodBotThreadSend {
			p = cl.params[i].(api.BotSendParams)
		}
	}
	if p != (api.BotSendParams{BotID: "n1:u", ThreadID: "n1:t1", Text: "go on"}) {
		t.Fatalf("send params = %+v", p)
	}
	cl.timeline.Sessions = nil
	m, _ = upd(m, m.loadTimelineCmd("n1:t1")())
	if m, _ = upd(m, keyMsg("o")); m.flash != "no live session in this thread" {
		t.Errorf("o without a live session: flash %q", m.flash)
	}
}

func threadViewModel() model {
	m := botViewModel()
	m.client = threadClient()
	th := newThreadComp(api.BotThreadInfo{ID: "n1:t1", BotID: "n1:u", Title: "flat search"})
	c := &ctx{m: &m}
	comp, _ := th.update(c, m.loadTimelineCmd("n1:t1")())
	m.main = m.main.push(comp)
	return m
}

func TestThreadViewDetail(t *testing.T) {
	m := openedThread(t, threadClient())
	m = typeKeys(m, "gg")
	m, _ = upd(m, keyMsg("enter"))
	if m.main.top().(threadComp).tr.historyView != histDetail {
		t.Fatal("enter opens the entry detail")
	}
	m = typeKeys(m, "jk")
	m, _ = upd(m, keyMsg("esc"))
	if th, ok := m.main.top().(threadComp); !ok || th.tr.historyView != histTranscript {
		t.Fatal("esc leaves the detail, not the thread")
	}
}

// toolThread is a thread of two sessions, each with one tool call: the first
// ended, the second live.
func toolThread() *botClient {
	cl := threadClient()
	ts := func(m int) string { return threadT0.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	cl.timeline = api.BotTimeline{ThreadID: "n1:t1",
		Entries: []transcript.Entry{
			{ID: "s0:u", Kind: transcript.EntryUser, Text: "list", Timestamp: ts(0)},
			{ID: "s0:t", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tool-old", InputPreview: "ls old", Timestamp: ts(1)},
			{ID: "s0:a", Kind: transcript.EntryText, Text: "listed", Timestamp: ts(2)},
			{ID: "s1:u", Kind: transcript.EntryUser, Text: "again", Timestamp: ts(3)},
			{ID: "s1:t", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tool-new", InputPreview: "ls new", Timestamp: ts(4)},
			{ID: "s1:a", Kind: transcript.EntryText, Text: "listed again", Timestamp: ts(5)},
		},
		Sessions: []api.BotTimelineSession{
			{SessionID: "n1:old", StartIndex: 0, CLI: "claude", TranscriptPath: "/t/old.jsonl"},
			{SessionID: "n1:new", StartIndex: 3, CLI: "claude", TranscriptPath: "/t/new.jsonl", Live: true},
		}}
	return cl
}

// drillTool opens the detail of the tool entry id and feeds it its body.
func drillTool(t *testing.T, m model, id string) model {
	t.Helper()
	th := m.main.top().(threadComp)
	v := th.bind(&ctx{m: &m})
	for _, r := range v.displayRows() {
		if r.run.key != "" {
			th.tr.transcript.runs[r.run.key] = true
		}
	}
	rows := v.displayRows()
	at := -1
	for i, r := range rows {
		if r.kind == entryRow && th.tr.transcript.entries[r.entry].ID == id {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no row for %s", id)
	}
	th.tr.transcript.cursor = at
	m.main = m.main.replaceTop(th)
	m, cmd := upd(m, keyMsg("enter"))
	if m.main.top().(threadComp).tr.historyView != histDetail || cmd == nil {
		t.Fatalf("enter on %s opens its detail and fetches the body", id)
	}
	m, _ = upd(m, cmd())
	return m
}

func TestThreadToolDetailEndedSession(t *testing.T) {
	cl := toolThread()
	m := openedThread(t, cl)
	m = drillTool(t, m, "s0:t")
	p, n := botParams[api.HistoryToolDetailParams](cl, api.MethodSessionHistoryToolDetail)
	if n != 1 || p.TranscriptPath != "/t/old.jsonl" || p.Agent != "claude" || p.ToolID != "tool-old" {
		t.Fatalf("history tool detail = %+v (%d calls)", p, n)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "detail from "+api.MethodSessionHistoryToolDetail) {
		t.Errorf("the detail lacks the body:\n%s", out)
	}
}

func TestThreadToolDetailLiveSession(t *testing.T) {
	cl := toolThread()
	m := openedThread(t, cl)
	m = drillTool(t, m, "s1:t")
	p, n := botParams[api.ToolDetailParams](cl, api.MethodSessionToolDetail)
	if n != 1 || p.SessionID != "n1:new" || p.ToolID != "tool-new" {
		t.Fatalf("session tool detail = %+v (%d calls)", p, n)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "detail from "+api.MethodSessionToolDetail) {
		t.Errorf("the detail lacks the body:\n%s", out)
	}
}

func TestThreadSubagentDrillReadsItsSession(t *testing.T) {
	cl := toolThread()
	cl.timeline.Entries[1] = transcript.Entry{ID: "s0:t", Kind: transcript.EntrySubagent, ToolName: "Task", ToolID: "tool-old",
		Subagents: []transcript.Subagent{{ID: "agent-7", Type: "explore", HasTrace: true}}, Timestamp: cl.timeline.Entries[1].Timestamp}
	m := openedThread(t, cl)
	m = drillTool(t, m, "s0:t")
	p, n := botParams[api.HistoryTranscriptParams](cl, api.MethodSessionsHistoryTranscript)
	if n != 1 || p.TranscriptPath != "/t/old.jsonl" || p.Agent != "claude" || p.AgentID != "agent-7" {
		t.Fatalf("subagent read = %+v (%d calls)", p, n)
	}
}
