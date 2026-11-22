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

func threadClient() *pagentClient {
	cl := agentViewClient()
	cl.timeline = api.PAgentTimeline{ThreadID: "n1:t1", Entries: threadEntries(),
		Sessions: []api.PAgentTimelineSession{{SessionID: "n1:s1", Live: true}}}
	return cl
}

// openedThread opens thread t1 from unready's agent view and loads it.
func openedThread(t *testing.T, cl *pagentClient) model {
	t.Helper()
	m := loadedAgentView(t, cl)
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
	m, _ = upd(m, m.applyEvent(api.Notification{Method: api.MethodPAgentChanged})())
	m, _ = upd(m, m.loadTimelineCmd("n1:t1")())
	if id := threadCursorID(m); id != "ev:7" {
		t.Fatalf("refresh moved the cursor to %q", id)
	}
}

func TestThreadViewMarksRead(t *testing.T) {
	cl := threadClient()
	m := openedThread(t, cl)
	var got []api.PAgentMarkReadParams
	collect := func() {
		got = nil
		for i, call := range cl.calls {
			if call == api.MethodPAgentThreadMarkRead {
				got = append(got, cl.params[i].(api.PAgentMarkReadParams))
			}
		}
	}
	// openedThread fed the load through upd, which ran no commands: run it here.
	th := m.main.top().(threadComp)
	th.readPos = 0
	c := &ctx{m: &m}
	_, cmd := th.update(c, m.loadTimelineCmd("n1:t1")())
	if cmd == nil {
		t.Fatal("a shown timeline marks the thread read")
	}
	cmd()
	collect()
	want := threadT0.Add(3 * time.Minute).UnixNano()
	if len(got) != 1 || got[0] != (api.PAgentMarkReadParams{ThreadID: "n1:t1", Position: want}) {
		t.Fatalf("markRead = %+v, want position %d", got, want)
	}
	th.readPos = want
	if _, cmd := th.update(c, m.loadTimelineCmd("n1:t1")()); cmd != nil {
		t.Error("an unchanged timeline marks it read again")
	}
}

func TestThreadViewUndoMemoryEvent(t *testing.T) {
	cl := threadClient()
	m := openedThread(t, cl)
	m, cmd := upd(m, keyMsg("u"))
	if cmd != nil || m.flash != "not a memory change" {
		t.Fatalf("u off a memory event: flash %q", m.flash)
	}
	m = typeKeys(m, "k")
	m, cmd = upd(m, keyMsg("u"))
	if cmd == nil {
		t.Fatal("u on the memory event undoes it")
	}
	cl.undoErr = errors.New("conflicts with a later change")
	m, _ = upd(m, cmd())
	var p api.PAgentMemoryUndoParams
	for i, call := range cl.calls {
		if call == api.MethodPAgentMemoryUndo {
			p = cl.params[i].(api.PAgentMemoryUndoParams)
		}
	}
	if p != (api.PAgentMemoryUndoParams{AgentID: "n1:u", Commit: "abc123"}) {
		t.Fatalf("undo params = %+v", p)
	}
	if !strings.Contains(m.flash, "conflicts with a later change") {
		t.Errorf("flash = %q", m.flash)
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
	var p api.PAgentSendParams
	for i, call := range cl.calls {
		if call == api.MethodPAgentThreadSend {
			p = cl.params[i].(api.PAgentSendParams)
		}
	}
	if p != (api.PAgentSendParams{AgentID: "n1:u", ThreadID: "n1:t1", Text: "go on"}) {
		t.Fatalf("send params = %+v", p)
	}
	cl.timeline.Sessions = nil
	m, _ = upd(m, m.loadTimelineCmd("n1:t1")())
	if m, _ = upd(m, keyMsg("o")); m.flash != "no live session in this thread" {
		t.Errorf("o without a live session: flash %q", m.flash)
	}
}

func threadViewModel() model {
	m := agentViewModel()
	m.client = threadClient()
	th := newThreadComp(api.PAgentThreadInfo{ID: "n1:t1", AgentID: "n1:u", Title: "flat search"})
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
func toolThread() *pagentClient {
	cl := threadClient()
	ts := func(m int) string { return threadT0.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	cl.timeline = api.PAgentTimeline{ThreadID: "n1:t1",
		Entries: []transcript.Entry{
			{ID: "s0:u", Kind: transcript.EntryUser, Text: "list", Timestamp: ts(0)},
			{ID: "s0:t", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tool-old", InputPreview: "ls old", Timestamp: ts(1)},
			{ID: "s0:a", Kind: transcript.EntryText, Text: "listed", Timestamp: ts(2)},
			{ID: "s1:u", Kind: transcript.EntryUser, Text: "again", Timestamp: ts(3)},
			{ID: "s1:t", Kind: transcript.EntryTool, ToolName: "Bash", ToolID: "tool-new", InputPreview: "ls new", Timestamp: ts(4)},
			{ID: "s1:a", Kind: transcript.EntryText, Text: "listed again", Timestamp: ts(5)},
		},
		Sessions: []api.PAgentTimelineSession{
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
	p, n := pagentParams[api.HistoryToolDetailParams](cl, api.MethodSessionHistoryToolDetail)
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
	p, n := pagentParams[api.ToolDetailParams](cl, api.MethodSessionToolDetail)
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
	p, n := pagentParams[api.HistoryTranscriptParams](cl, api.MethodSessionsHistoryTranscript)
	if n != 1 || p.TranscriptPath != "/t/old.jsonl" || p.Agent != "claude" || p.AgentID != "agent-7" {
		t.Fatalf("subagent read = %+v (%d calls)", p, n)
	}
}
