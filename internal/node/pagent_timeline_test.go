package node

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
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

func timelineThread(t *testing.T, r *threadRig, views map[string][]transcript.Entry, sessions ...pagent.ThreadSession) {
	t.Helper()
	ctx := context.Background()
	r.d.adapters["claude"] = pathAdapter{Adapter: r.d.adapters["claude"], views: views}
	if err := r.store.InsertThread(ctx, pagent.Thread{ID: "th_x", AgentID: r.agent.ID, Kind: threadChat, StartedBy: startedByYou}); err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		s.ThreadID, s.CLI, s.StartedBy = "th_x", "claude", startedByYou
		if err := r.store.InsertThreadSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
}

func timeline(t *testing.T, r *threadRig) api.PAgentTimeline {
	t.Helper()
	res, err := callPAgent[api.PAgentTimeline](t, r.d.handlePAgentThreadTimeline, api.PAgentTimelineParams{ThreadID: "th_x"})
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
		pagent.ThreadSession{SessionID: "old1", AgentSessionID: "a", TranscriptPath: "/a"},
		pagent.ThreadSession{SessionID: "old2", AgentSessionID: "b", TranscriptPath: "/b"},
		// resumed: same transcript as old2
		pagent.ThreadSession{SessionID: "old3", AgentSessionID: "b", TranscriptPath: "/b"},
	)
	res := timeline(t, r)
	eqIDs(t, res.Entries, "s0:1", "s0:2", "s1:1", "s1:2", "s1:3")
	if len(res.Sessions) != 3 || res.Sessions[0].StartIndex != 0 || res.Sessions[1].StartIndex != 2 || res.Sessions[2].StartIndex != 5 {
		t.Fatalf("sessions = %+v", res.Sessions)
	}
}

func TestTimelineSplicesEventsAtAnchor(t *testing.T) {
	r := newThreadRig(t)
	timelineThread(t, r, map[string][]transcript.Entry{"/a": tl("1", "2"), "/b": tl("1")},
		pagent.ThreadSession{SessionID: "s1", TranscriptPath: "/a"},
		pagent.ThreadSession{SessionID: "s2", TranscriptPath: "/b"},
	)
	ctx := context.Background()
	for _, e := range []struct {
		kind, sess string
		idx        int
	}{{"session.started", "s1", 0}, {"note", "s1", 1}, {"late", "s2", 99}} {
		if err := r.d.pagentEvent(ctx, "th_x", e.kind, startedByYou, map[string]string{"reason": "r"}, e.sess, e.idx); err != nil {
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
		pagent.ThreadSession{SessionID: "s1", AgentSessionID: "a", TranscriptPath: "/a"},
	)
	// the resumed session started after the first two entries
	if err := r.store.InsertThreadSession(context.Background(), pagent.ThreadSession{
		ThreadID: "th_x", SessionID: "s2", CLI: "claude", AgentSessionID: "a", TranscriptPath: "/a", StartedBy: startedByYou,
	}); err != nil {
		t.Fatal(err)
	}
	links := r.links(t, "th_x")
	links[1].StartedAt = base.Add(90 * time.Second)
	ev := []pagent.Event{{ID: 7, Kind: "session.resumed", AnchorSession: "s2", CreatedAt: links[1].StartedAt}}
	res := r.d.buildTimeline("th_x", links, ev)
	eqIDs(t, res.Entries, "s0:1", "s0:2", "ev:7", "s0:3")
}

func TestTimelineMissingTranscriptAddsErrorEntry(t *testing.T) {
	r := newThreadRig(t)
	timelineThread(t, r, map[string][]transcript.Entry{"/b": tl("1")},
		pagent.ThreadSession{SessionID: "s1", TranscriptPath: "/gone"},
		pagent.ThreadSession{SessionID: "s2", TranscriptPath: "/b"},
	)
	res := timeline(t, r)
	eqIDs(t, res.Entries, "s0:err", "s1:1")
	if e := res.Entries[0]; e.Kind != transcript.EntryArgus || !e.IsError {
		t.Fatalf("entry = %+v", e)
	}
}
