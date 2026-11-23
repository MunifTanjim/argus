package node

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// pathAdapter serves transcripts by path.
type pathAdapter struct {
	adapter.Adapter
	views map[string][]transcript.Entry
}

func (a pathAdapter) ReadTranscriptView(path string) (transcript.TranscriptView, error) {
	e, ok := a.views[path]
	if !ok {
		return transcript.TranscriptView{}, os.ErrNotExist
	}
	return transcript.TranscriptView{Entries: e}, nil
}

func tl(ids ...string) []transcript.Entry {
	var out []transcript.Entry
	for _, id := range ids {
		out = append(out, transcript.Entry{ID: id, Kind: transcript.EntryText, Text: id})
	}
	return out
}

func timelineThread(t *testing.T, r *threadRig, views map[string][]transcript.Entry, sessions ...bot.ThreadSession) {
	t.Helper()
	ctx := context.Background()
	r.d.adapters["claude"] = pathAdapter{Adapter: r.d.adapters["claude"], views: views}
	if err := r.store.InsertThread(ctx, bot.Thread{ID: "th_x", BotID: r.bot.ID, Kind: threadChat, StartedBy: startedByYou}); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		s.ThreadID, s.CLI, s.StartedBy = "th_x", "claude", startedByYou
		if err := r.store.InsertThreadSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
}

func timeline(t *testing.T, r *threadRig) api.BotTimeline {
	t.Helper()
	res, err := callBot[api.BotTimeline](t, r.d.handleBotThreadTimeline, api.BotTimelineParams{ThreadID: "th_x"})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func entryIDs(es []transcript.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func eqIDs(t *testing.T, got []transcript.Entry, want ...string) {
	t.Helper()
	g := entryIDs(got)
	if len(g) != len(want) {
		t.Fatalf("entries = %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("entries = %v, want %v", g, want)
		}
	}
}

func TestTimelineMergesSessionsInOrder(t *testing.T) {
	r := newThreadRig(t)
	timelineThread(t, r, map[string][]transcript.Entry{"/a": tl("1", "2"), "/b": tl("1", "2", "3")},
		bot.ThreadSession{SessionID: "old1", AgentSessionID: "a", TranscriptPath: "/a"},
		bot.ThreadSession{SessionID: "old2", AgentSessionID: "b", TranscriptPath: "/b"},
		// resumed: same transcript as old2
		bot.ThreadSession{SessionID: "old3", AgentSessionID: "b", TranscriptPath: "/b"},
	)
	res := timeline(t, r)
	eqIDs(t, res.Entries, "s0:1", "s0:2", "s1:1", "s1:2", "s1:3")
	if len(res.Sessions) != 3 || res.Sessions[0].StartIndex != 0 || res.Sessions[1].StartIndex != 2 || res.Sessions[2].StartIndex != 5 {
		t.Fatalf("sessions = %+v", res.Sessions)
	}
	if s := res.Sessions[1]; s.TranscriptPath != "/b" || s.CLI != "claude" {
		t.Fatalf("session 1 lacks its transcript address: %+v", s)
	}
}

func TestTimelineSplicesEventsAtAnchor(t *testing.T) {
	r := newThreadRig(t)
	timelineThread(t, r, map[string][]transcript.Entry{"/a": tl("1", "2"), "/b": tl("1")},
		bot.ThreadSession{SessionID: "s1", TranscriptPath: "/a"},
		bot.ThreadSession{SessionID: "s2", TranscriptPath: "/b"},
	)
	ctx := context.Background()
	for _, e := range []struct {
		kind, sess string
		idx        int
	}{{"session.started", "s1", 0}, {"note", "s1", 1}, {"late", "s2", 99}} {
		if err := r.d.botEvent(ctx, "th_x", e.kind, startedByYou, map[string]string{"reason": "r"}, e.sess, e.idx); err != nil {
			t.Fatal(err)
		}
	}
	res := timeline(t, r)
	eqIDs(t, res.Entries, "ev:1", "s0:1", "ev:2", "s0:2", "s1:1", "ev:3")
	ev := res.Entries[0]
	if ev.Kind != transcript.EntryArgus || ev.Label != "session.started" || ev.Event == nil || ev.Event.ID != 1 {
		t.Fatalf("event entry = %+v", ev)
	}
}

func TestTimelineResumedSessionEventLandsAfterSharedHistory(t *testing.T) {
	r := newThreadRig(t)
	base := time.Now().Add(-time.Hour)
	entries := tl("1", "2", "3")
	for i := range entries {
		entries[i].Timestamp = base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
	}
	timelineThread(t, r, map[string][]transcript.Entry{"/a": entries},
		bot.ThreadSession{SessionID: "s1", AgentSessionID: "a", TranscriptPath: "/a"},
	)
	// the resumed session started after the first two entries
	if err := r.store.InsertThreadSession(context.Background(), bot.ThreadSession{
		ThreadID: "th_x", SessionID: "s2", CLI: "claude", AgentSessionID: "a", TranscriptPath: "/a", StartedBy: startedByYou,
	}); err != nil {
		t.Fatal(err)
	}
	links := r.links(t, "th_x")
	links[1].StartedAt = base.Add(90 * time.Second)
	ev := []bot.Event{{ID: 7, Kind: "session.resumed", AnchorSession: "s2", CreatedAt: links[1].StartedAt}}
	res := r.d.buildTimeline("th_x", links, ev)
	eqIDs(t, res.Entries, "s0:1", "s0:2", "ev:7", "s0:3")
}

func TestTimelineMissingTranscriptAddsErrorEntry(t *testing.T) {
	r := newThreadRig(t)
	timelineThread(t, r, map[string][]transcript.Entry{"/b": tl("1")},
		bot.ThreadSession{SessionID: "s1", TranscriptPath: "/gone"},
		bot.ThreadSession{SessionID: "s2", TranscriptPath: "/b"},
	)
	res := timeline(t, r)
	eqIDs(t, res.Entries, "s0:err", "s1:1")
	if e := res.Entries[0]; e.Kind != transcript.EntryArgus || !e.IsError {
		t.Fatalf("entry = %+v", e)
	}
}

func TestEventTextReadsPayloads(t *testing.T) {
	for _, c := range []struct {
		kind, payload, want string
	}{
		{evMemoryChanged, `{"summary":"added flat search","ops":[{}]}`, "Memory: added flat search"},
		{evMemoryProposed, `{"ops":[{},{}]}`, "Memory: 2 changes to review"},
		{evMemoryUndone, `{"commit":"abc","revert":"def"}`, "Undid a change"},
		{"session.continued", `{"reason":"context full"}`, "Continued in a new session: context full"},
		{evRoutineResult, `{"routine":"brief","result":"done","summary":"sent the brief","actions_done":["x"]}`, "Routine brief done: sent the brief"},
		{evRoutineNeedsYou, `{"routine":"brief","summary":"login expired"}`, "Routine brief needs you: login expired"},
		{evRoutineCompleted, `{"routine":"billing"}`, "Routine billing reached its goal"},
		{evRoutineFailing, `{"routine":"brief","error":"exit 1"}`, "Routine brief failed: exit 1"},
		{evRoutineChanged, `{"routine":"brief","action":"created","commit":"abc"}`, "Routine brief created"},
		{evRoutineProposed, `{"routine":"brief","reason":"run earlier"}`, "Routine brief change to review: run earlier"},
		{evRoutineError, `{"routine":"brief","message":"bad schedule"}`, "Routine brief error: bad schedule"},
		{evRoutineMissed, `{"routine":"brief","count":3}`, "Routine brief missed 3 runs"},
		{evRoutineSkipped, `{"routine":"brief","reason":"overlap"}`, "Routine brief skipped a run: overlap"},
		{evTokenBudgetUnavailable, `{"routine":"billing","cli":"antigravity"}`, "Routine billing: token budget unavailable for antigravity"},
		{evRoutineStale, `{"routine":"brief","due":"2026-11-22T10:00:00Z"}`, "Routine brief is overdue since 2026-11-22 10:00 UTC"},
		{evRoutinePaused, `{"reason":"away","routines":["a","b"]}`, "Paused a, b: away"},
		{evRoutinePaused, `{"reason":"failures","routines":["a"],"message":"3 runs in a row failed"}`, "Paused a: failures: 3 runs in a row failed"},
		{evDraft, `{"tool":"Bash","command":"git push","classes":["publish"]}`, "Drafted Bash: git push"},
		{evDraftSent, `{"event_id":4,"command":"git push","exit_code":0}`, "Ran approved command: git push"},
		{evDraftDiscarded, `{"event_id":4}`, "Discarded a drafted action"},
		{evSkillsMissing, `{"skills":["x"],"message":"Skills not found: x"}`, "Skills not found: x"},
		{evHandoffAsked, `{"bot":"researcher","task":"find the date"}`, "Asked researcher: find the date"},
		{evHandoffAnswered, `{"bot":"researcher","text":"3 March.\nSource: the press page."}`, "Answer from researcher: 3 March.\nSource: the press page."},
		{evHandoffRefused, `{"bot":"x","reason":"can't ask \"x\": it is not a bot you may message"}`, `can't ask "x": it is not a bot you may message`},
		{evHandoffNoAnswer, `{"bot":"researcher","text":"no answer within 1h"}`, "No answer from researcher: no answer within 1h"},
		{evBrief, `{"run_id":"rr1","text":"Morning.\nThe mail site needs your login."}`, "Morning.\nThe mail site needs your login."},
		{evOffer, `{"offer_id":"of1","title":"Tidy the inbox","details":"120 unread","prompt":"Please tidy my inbox."}`, "Offer: Tidy the inbox: 120 unread"},
		{evOfferStarted, `{"offer_id":"of1","title":"Tidy the inbox"}`, "Started the offer: Tidy the inbox"},
		{evOfferDismissed, `{"offer_id":"of1"}`, "Dismissed the offer"},
		{evHealth, `{"key":"cli-missing","message":"claude is not installed on this node"}`, "Health: claude is not installed on this node"},
		{"other.kind", `{}`, "other.kind"},
	} {
		if got := eventText(bot.Event{Kind: c.kind, Payload: json.RawMessage(c.payload)}); got != c.want {
			t.Errorf("%s: %q, want %q", c.kind, got, c.want)
		}
	}
}
