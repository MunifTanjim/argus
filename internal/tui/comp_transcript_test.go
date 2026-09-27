package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

type streamClient struct {
	recordingClient
	hist map[string][]transcript.Chunk
}

func (c *streamClient) Call(method string, params, result any) error {
	_ = c.recordingClient.Call(method, params, result)
	v, ok := result.(*transcript.TranscriptView)
	p, isHist := params.(api.HistoryTranscriptParams)
	if ok && isHist {
		key := p.TranscriptPath
		if p.AgentID != "" {
			key += "#" + p.AgentID
		}
		v.Chunks = c.hist[key]
	}
	return nil
}

func (c *recordingClient) subIDs(method string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for i, name := range c.calls {
		if name != method {
			continue
		}
		switch p := c.params[i].(type) {
		case api.TranscriptSubscribeParams:
			out = append(out, p.SubID)
		case api.TranscriptUnsubscribeParams:
			out = append(out, p.SubID)
		}
	}
	return out
}

func userChunk(id, text string) transcript.Chunk {
	return transcript.Chunk{ID: id, Kind: transcript.ChunkUser, Text: text}
}

// liveDeltas fills in each catch-up's stream and chunks, as the node answers a
// subscribe.
func liveDeltas(m model, cmd tea.Cmd, chunks []transcript.Chunk) model {
	for _, msg := range execCmd(cmd) {
		if d, ok := msg.(transcriptDeltaMsg); ok {
			d.delta.SubID, d.delta.Chunks = d.ref.subID, chunks
			m, _ = upd(m, d)
		}
	}
	return m
}

func resumeInto(m model, id string, chunks ...transcript.Chunk) model {
	m, cmd := upd(m, resumeResultMsg{sessionID: id})
	return liveDeltas(m, cmd, chunks)
}

func streamModel(c Client) model {
	m := wideWorkspace()
	m.client = c
	m.transcriptCache = map[string]cachedTranscript{}
	m.sessions["n1:s9"] = session.Session{ID: "n1:s9", WorkspaceID: "n1:w1", Repo: "repo"}
	return m
}

func subagentChunk() transcript.Chunk {
	return transcript.Chunk{ID: "a", Kind: transcript.ChunkAI, Items: []transcript.Item{{
		Kind:      transcript.ItemSubagent,
		Subagents: []transcript.Subagent{{Type: "explorer", HasTrace: true, ID: "agent42"}},
	}}}
}

// drillSubagent opens the card detail and drills into the streamed subagent.
func drillSubagent(t *testing.T, m model) model {
	t.Helper()
	m = pressKeys(m, keyMsg("enter"))
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	return m
}

func viewText(m model) string { return ansi.Strip(m.View().Content) }

func assertUnsubscribed(t *testing.T, rc *recordingClient, ids ...string) {
	t.Helper()
	got := rc.subIDs(api.MethodTranscriptUnsubscribe)
	for _, id := range ids {
		if !slices.Contains(got, id) {
			t.Errorf("stream %q should be unsubscribed; unsubscribed %v", id, got)
		}
	}
}

func TestLateHistoryReplyLeavesTheLiveTranscript(t *testing.T) {
	c := &streamClient{hist: map[string][]transcript.Chunk{"/p/old.jsonl": {userChunk("h1", "from history")}}}
	m := testModel()
	m.client = c
	m.transcriptCache = map[string]cachedTranscript{}
	m.sessions = map[string]session.Session{"s1": {ID: "s1"}}
	p := session.HistoryProject{NodeID: "n1", ProjectDir: "/p", Label: "proj"}
	s := session.HistorySession{SessionID: "old", TranscriptPath: "/p/old.jsonl", Agent: "claude"}
	m = withHistorySessions(m, p, session.HistorySessionPage{Items: []session.HistorySession{s}})
	m, fetch := upd(m, keyMsg("enter"))
	m = pressKeys(m, keyMsg("esc"))
	m = resumeInto(m, "s1", userChunk("l1", "live text"))
	if !strings.Contains(viewText(m), "live text") {
		t.Fatalf("the live transcript should show its chunks:\n%s", viewText(m))
	}
	for _, msg := range execCmd(fetch) {
		m, _ = upd(m, msg)
	}
	out := viewText(m)
	if !strings.Contains(out, "live text") || strings.Contains(out, "from history") {
		t.Errorf("a late history reply must leave the live transcript as it was:\n%s", out)
	}
}

func TestHistoryReplyReachesItsTranscript(t *testing.T) {
	c := &streamClient{hist: map[string][]transcript.Chunk{"/p/old.jsonl": {userChunk("h1", "from history")}}}
	m := testModel()
	m.client = c
	m.transcriptCache = map[string]cachedTranscript{}
	p := session.HistoryProject{NodeID: "n1", ProjectDir: "/p", Label: "proj"}
	s := session.HistorySession{SessionID: "old", TranscriptPath: "/p/old.jsonl", Agent: "claude"}
	m = withHistorySessions(m, p, session.HistorySessionPage{Items: []session.HistorySession{s}})
	m, fetch := upd(m, keyMsg("enter"))
	for _, msg := range execCmd(fetch) {
		m, _ = upd(m, msg)
	}
	if !strings.Contains(viewText(m), "from history") {
		t.Errorf("the history transcript should show its reply:\n%s", viewText(m))
	}
}

func TestLateLiveReplyLeavesTheHistoryTranscript(t *testing.T) {
	c := &streamClient{hist: map[string][]transcript.Chunk{"/p/old.jsonl": {userChunk("h1", "from history")}}}
	m := testModel()
	m.client = c
	m.transcriptCache = map[string]cachedTranscript{}
	m.sessions = map[string]session.Session{"s1": {ID: "s1"}}
	m = resumeInto(m, "s1", userChunk("l1", "live text"))
	m = pressKeys(m, keyMsg("esc"))
	p := session.HistoryProject{NodeID: "n1", ProjectDir: "/p", Label: "proj"}
	s := session.HistorySession{SessionID: "old", TranscriptPath: "/p/old.jsonl", Agent: "claude"}
	m = withHistorySessions(m, p, session.HistorySessionPage{Items: []session.HistorySession{s}})
	m, fetch := upd(m, keyMsg("enter"))
	for _, msg := range execCmd(fetch) {
		m, _ = upd(m, msg)
	}
	m, _ = upd(m, transcriptMsg{id: "s1", chunks: []transcript.Chunk{userChunk("l2", "late live")}})
	out := viewText(m)
	if !strings.Contains(out, "from history") || strings.Contains(out, "late live") {
		t.Errorf("a late live reply must leave the history transcript as it was:\n%s", out)
	}
}

func TestBackClosesTheSessionStream(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1")
	sub := rc.subIDs(api.MethodTranscriptSubscribe)
	m, cmd := upd(m, keyMsg("esc"))
	runCmd(cmd)
	if viewOf(m) == viewSession {
		t.Fatal("back should leave the session")
	}
	assertUnsubscribed(t, rc, sub...)
}

func TestOpeningAnotherRowClosesBothStreams(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentChunk())
	m = drillSubagent(t, m)
	subs := rc.subIDs(api.MethodTranscriptSubscribe)
	if len(subs) != 2 {
		t.Fatalf("setup: want the session and the subagent streams, got %v", subs)
	}
	m = pressKeys(m, cw('h')...)
	if viewOf(m) != viewSession || m.focused != leftSidebar {
		t.Fatalf("focus left should keep the session: view=%v focus=%v", viewOf(m), m.focused)
	}
	if got := rc.subIDs(api.MethodTranscriptUnsubscribe); len(got) != 0 {
		t.Fatalf("focus left closed streams %v", got)
	}
	m = typeKeys(m, "j")
	m, cmd := upd(m, keyMsg("enter"))
	runCmd(cmd)
	if viewOf(m) != viewTree {
		t.Fatalf("<CR> on n1:w2 should open its pane: view=%v", viewOf(m))
	}
	assertUnsubscribed(t, rc, subs...)
}

func TestOpeningAnotherRowUnderAFileClosesTheStream(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1")
	m = withFile(m, fileComp{ws: "n1:w1", path: "go.mod"})
	m = withFocus(m, mainPane)
	sub := rc.subIDs(api.MethodTranscriptSubscribe)
	cmd := m.openRow("n1:w2")
	runCmd(cmd)
	if viewOf(m) != viewTree || !m.hasOpenFile() {
		t.Fatalf("the new stack should keep the file open: view=%v file=%v", viewOf(m), m.hasOpenFile())
	}
	assertUnsubscribed(t, rc, sub...)
}

func TestResumeWhileOpenClosesTheOldStream(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1")
	old := rc.subIDs(api.MethodTranscriptSubscribe)
	m, cmd := upd(m, resumeResultMsg{sessionID: "n1:s9"})
	runCmd(cmd)
	if viewOf(m) != viewSession || m.liveSessionID() != "n1:s9" {
		t.Fatalf("resume should open n1:s9: view=%v id=%q", viewOf(m), m.liveSessionID())
	}
	assertUnsubscribed(t, rc, old...)
	if got := rc.subIDs(api.MethodTranscriptUnsubscribe); len(got) != len(old) {
		t.Errorf("only the old streams close: unsubscribed %v, old %v", got, old)
	}
	m = pressKeys(m, keyMsg("esc"))
	if viewOf(m) == viewSession {
		t.Error("back from the resumed session returns to where the old one came from, not to it")
	}
}

func TestEnterSessionIntoAnotherClosesTheOldStreams(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentChunk())
	m = drillSubagent(t, m)
	old := rc.subIDs(api.MethodTranscriptSubscribe)
	m, cmd := m.enterSession("n1:s9")
	runCmd(cmd)
	assertUnsubscribed(t, rc, old...)
	if m.liveSessionID() != "n1:s9" {
		t.Errorf("liveSessionID = %q, want n1:s9", m.liveSessionID())
	}
}

func TestQuitClosesBothStreams(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentChunk())
	m = drillSubagent(t, m)
	subs := rc.subIDs(api.MethodTranscriptSubscribe)
	_, cmd := upd(m, ctrlKey('c'))
	var quit bool
	for _, msg := range execCmd(cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			quit = true
		}
	}
	if !quit {
		t.Error("ctrl+c should still quit")
	}
	assertUnsubscribed(t, rc, subs...)
}

func TestRebuildingTheStackClosesTheStream(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1")
	sub := rc.subIDs(api.MethodTranscriptSubscribe)
	cmd := m.showHome()
	runCmd(cmd)
	if viewOf(m) != viewHome {
		t.Fatalf("view = %v, want Home", viewOf(m))
	}
	assertUnsubscribed(t, rc, sub...)
}

func TestDeltaForAClosedStreamIsDropped(t *testing.T) {
	rc := &recordingClient{}
	m, cmd := upd(streamModel(rc), resumeResultMsg{sessionID: "n1:s1"})
	var late []transcriptDeltaMsg
	for _, msg := range execCmd(cmd) {
		if d, ok := msg.(transcriptDeltaMsg); ok {
			d.delta.Chunks = []transcript.Chunk{userChunk("l1", "first session")}
			late = append(late, d)
		}
	}
	m = resumeInto(m, "n1:s9", userChunk("l9", "second session"))
	for _, d := range late {
		m, _ = upd(m, d)
	}
	out := viewText(m)
	if !strings.Contains(out, "second session") || strings.Contains(out, "first session") {
		t.Errorf("a delta for the closed stream must be dropped:\n%s", out)
	}
}

func TestLiveScreenKeepsTheStreamOpen(t *testing.T) {
	rc := &recordingClient{}
	m := streamModel(rc)
	s := m.sessions["n1:s1"]
	s.CanOpenTerminal = true
	m.sessions["n1:s1"] = s
	m = resumeInto(m, "n1:s1", userChunk("l1", "live text"))
	sub := trOf(m).activeSub
	m, cmd := upd(m, ctrlKey('t'))
	runCmd(cmd)
	if viewOf(m) != viewScreen {
		t.Fatalf("<C-t>: view = %v, want the live screen", viewOf(m))
	}
	m, cmd = upd(m, tea.KeyPressMsg{Code: ']', Mod: tea.ModCtrl})
	runCmd(cmd)
	if viewOf(m) != viewSession || trOf(m).activeSub != sub {
		t.Fatalf("^]: view = %v stream = %+v, want the session on %+v", viewOf(m), trOf(m).activeSub, sub)
	}
	if got := rc.subIDs(api.MethodTranscriptUnsubscribe); len(got) != 0 {
		t.Errorf("the live screen round trip must keep the stream open: unsubscribed %v", got)
	}
	if !strings.Contains(viewText(m), "live text") {
		t.Errorf("the transcript should come back as it was:\n%s", viewText(m))
	}
}

var histProj = session.HistoryProject{NodeID: "n1", ProjectDir: "/p", Label: "proj"}

func histSession(id string) session.HistorySession {
	return session.HistorySession{SessionID: id, TranscriptPath: "/p/" + id + ".jsonl", Agent: "claude"}
}

func historyModel(c Client, ids ...string) model {
	m := testModel()
	m.client = c
	m.transcriptCache = map[string]cachedTranscript{}
	m.sessions = map[string]session.Session{"s1": {ID: "s1"}}
	var page session.HistorySessionPage
	for _, id := range ids {
		page.Items = append(page.Items, histSession(id))
	}
	return withHistorySessions(m, histProj, page)
}

func deliver(m model, cmd tea.Cmd) model {
	for _, msg := range execCmd(cmd) {
		if msg != nil {
			m, _ = upd(m, msg)
		}
	}
	return m
}

func toolChunk() transcript.Chunk {
	return transcript.Chunk{ID: "a", Kind: transcript.ChunkAI, Items: []transcript.Item{
		{Kind: transcript.ItemTool, ToolName: "Bash", ToolID: "tool1", InputPreview: "ls"},
	}}
}

// expandTool returns the tool body's fetch without running it.
func expandTool(m model) (model, tea.Cmd) {
	m = pressKeys(m, keyMsg("enter"))
	return upd(m, keyMsg("l"))
}

func TestLateReplyForAnotherHistoryTranscriptIsDropped(t *testing.T) {
	c := &streamClient{hist: map[string][]transcript.Chunk{
		"/p/a.jsonl": {userChunk("a1", "from a")},
		"/p/b.jsonl": {userChunk("b1", "from b")},
	}}
	m, fetchA := upd(historyModel(c, "a", "b"), keyMsg("enter"))
	m = pressKeys(m, keyMsg("esc"), keyMsg("j"))
	m, fetchB := upd(m, keyMsg("enter"))
	m = deliver(m, fetchB)
	m = deliver(m, fetchA)
	out := viewText(m)
	if !strings.Contains(out, "from b") || strings.Contains(out, "from a") {
		t.Errorf("a reply for a closed history transcript must leave another one as it was:\n%s", out)
	}
}

func TestLateReplyForAnotherLiveSessionIsDropped(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", userChunk("l1", "first session"))
	m = resumeInto(m, "n1:s9", userChunk("l9", "second session"))
	m, _ = upd(m, transcriptMsg{id: "n1:s1", chunks: []transcript.Chunk{userChunk("x", "late first")}})
	out := viewText(m)
	if !strings.Contains(out, "second session") || strings.Contains(out, "late first") {
		t.Errorf("a reply for the closed session must leave the open one as it was:\n%s", out)
	}
}

func TestLateSubagentReplyForAnotherHistoryTranscriptIsDropped(t *testing.T) {
	sub := func(text string) []transcript.Chunk {
		return []transcript.Chunk{{ID: "x", Kind: transcript.ChunkAI, Items: []transcript.Item{{Kind: transcript.ItemText, Text: text}}}}
	}
	c := &streamClient{hist: map[string][]transcript.Chunk{
		"/p/a.jsonl":         {subagentChunk()},
		"/p/b.jsonl":         {subagentChunk()},
		"/p/a.jsonl#agent42": sub("from a sub"),
		"/p/b.jsonl#agent42": sub("from b sub"),
	}}
	m, fetch := upd(historyModel(c, "a", "b"), keyMsg("enter"))
	m = deliver(m, fetch)
	m = pressKeys(m, keyMsg("enter"))
	m, lateA := upd(m, keyMsg("enter"))
	m = pressKeys(m, keyMsg("esc"), keyMsg("esc"), keyMsg("esc"), keyMsg("j"))
	m, fetch = upd(m, keyMsg("enter"))
	m = deliver(m, fetch)
	m = pressKeys(m, keyMsg("enter"))
	m, subB := upd(m, keyMsg("enter"))
	m = deliver(m, lateA)
	if out := viewText(m); strings.Contains(out, "from a sub") {
		t.Fatalf("a subagent reply for a closed history transcript must not fill another one:\n%s", out)
	}
	m = deliver(m, subB)
	if out := viewText(m); !strings.Contains(out, "from b sub") {
		t.Errorf("the transcript's own subagent reply should fill its frame:\n%s", out)
	}
}

func TestHistoryToolReplyLeavesTheLiveTranscript(t *testing.T) {
	c := &streamClient{hist: map[string][]transcript.Chunk{"/p/a.jsonl": {toolChunk()}}}
	m, fetch := upd(historyModel(c, "a"), keyMsg("enter"))
	m = deliver(m, fetch)
	m, histTool := expandTool(m)
	m = resumeInto(m, "s1", toolChunk())
	m, _ = expandTool(m)
	m = deliver(m, histTool)
	if e := trOf(m).toolBodies["tool1"]; !e.loading || e.done {
		t.Fatalf("a history tool reply must leave the live transcript's fetch outstanding: %+v", e)
	}
	m = pressKeys(m, keyMsg("esc"), keyMsg("esc"))
	if viewOf(m) != viewHistoryTranscript || !trOf(m).toolBodies["tool1"].done {
		t.Fatalf("back: view = %v tool = %+v, want the history transcript with its reply", viewOf(m), trOf(m).toolBodies["tool1"])
	}
	if out := viewText(m); strings.Contains(out, "loading…") {
		t.Errorf("the history tool body should not stay loading:\n%s", out)
	}
}

func TestToolReplyForAnotherLiveSessionIsDropped(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", toolChunk())
	m, late := expandTool(m)
	m = resumeInto(m, "n1:s9", toolChunk())
	m, _ = expandTool(m)
	m = deliver(m, late)
	if e := trOf(m).toolBodies["tool1"]; !e.loading || e.done {
		t.Errorf("a tool reply for the closed session must leave the open one's fetch outstanding: %+v", e)
	}
}

func TestSubagentDeltaFillsItsFrameAndBackResubscribes(t *testing.T) {
	rc := &recordingClient{}
	m := resumeInto(streamModel(rc), "n1:s1", subagentChunk())
	m = pressKeys(m, keyMsg("enter"))
	m, cmd := upd(m, keyMsg("enter"))
	m = liveDeltas(m, cmd, []transcript.Chunk{{ID: "x", Kind: transcript.ChunkAI, Items: []transcript.Item{
		{Kind: transcript.ItemText, Text: "subagent says hi"},
	}}})
	if out := viewText(m); !strings.Contains(out, "subagent says hi") {
		t.Fatalf("the subagent delta should fill its frame:\n%s", out)
	}
	subs := rc.subIDs(api.MethodTranscriptSubscribe)
	if len(subs) != 2 {
		t.Fatalf("setup: want the session and the subagent streams, got %v", subs)
	}
	m, cmd = upd(m, keyMsg("esc"))
	runCmd(cmd)
	assertUnsubscribed(t, rc, subs[1])
	if got := rc.subIDs(api.MethodTranscriptSubscribe); len(got) != 3 || got[2] != subs[0] {
		t.Errorf("back from the subagent should resubscribe the session stream %q: subscribed %v", subs[0], got)
	}
	if trOf(m).activeSub.subID != subs[0] || trOf(m).sessionSub.subID != "" {
		t.Errorf("back from the subagent: activeSub = %+v sessionSub = %+v", trOf(m).activeSub, trOf(m).sessionSub)
	}
}

func TestClearThroughUpdateMovesTheStream(t *testing.T) {
	rc := &recordingClient{}
	m := streamModel(rc)
	s := m.sessions["n1:s1"]
	s.AgentSessionID = "c0"
	m.sessions["n1:s1"] = s
	m = resumeInto(m, "n1:s1", userChunk("l1", "before clear"))
	old := trOf(m).activeSub
	s.AgentSessionID = "c1"
	params, _ := json.Marshal(registry.Event{Type: registry.EventUpdated, Session: s})
	m, cmd := upd(m, notificationMsg(api.Notification{Method: api.MethodSessionEvent, Params: params}))
	runCmd(cmd)
	if sub := trOf(m).activeSub; sub.subID == old.subID || sub.cacheKey != "c1" {
		t.Fatalf("/clear should move the transcript to a new stream on c1: %+v", sub)
	}
	assertUnsubscribed(t, rc, old.subID)
}
