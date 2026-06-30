package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

// Fanout calls every source and tags each reply with its origin node, the basis
// for aggregating history projects across machines.
func TestFanoutTagsResultsByNode(t *testing.T) {
	a := New(0)

	srcA := newFakeSource("home", "home-box")
	srcA.callResp, _ = json.Marshal([]session.HistoryProject{{ProjectDir: "/a", Label: "projA"}})
	srcB := newFakeSource("work", "work-box")
	srcB.callResp, _ = json.Marshal([]session.HistoryProject{{ProjectDir: "/b", Label: "projB"}})
	a.AddSource(srcA)
	a.AddSource(srcB)

	results := a.Fanout(context.Background(), "sessions.historyProjects", nil)
	if len(results) != 2 {
		t.Fatalf("want 2 fanout results, got %d", len(results))
	}

	byNode := map[string]FanoutResult{}
	for _, r := range results {
		byNode[r.NodeID] = r
	}
	for id, wantLabel := range map[string]string{"home": "home-box", "work": "work-box"} {
		r, ok := byNode[id]
		if !ok {
			t.Fatalf("missing fanout result for node %q", id)
		}
		if r.Err != nil {
			t.Errorf("%s: unexpected err %v", id, r.Err)
		}
		if r.Label != wantLabel {
			t.Errorf("%s: label = %q, want %q", id, r.Label, wantLabel)
		}
		var projects []session.HistoryProject
		if err := json.Unmarshal(r.Result, &projects); err != nil || len(projects) != 1 {
			t.Errorf("%s: result did not decode to one project: %v", id, err)
		}
	}
}

// historySessions routes to one node; the gateway must stamp each returned session
// with its owning node so a client can address its transcript by node_id.
func TestHistorySessionsStampsNodeID(t *testing.T) {
	a := New(time.Second)
	home := newFakeSource("home", "home-box", sess("default:%1"))
	home.callResp, _ = json.Marshal(session.HistorySessionPage{
		Items:   []session.HistorySession{{SessionID: "s1", TranscriptPath: "/t/s1.jsonl"}},
		HasMore: false,
	})
	a.AddSource(home)
	eventually(t, func() bool { return len(a.Snapshot()) == 1 })

	srv := NewServer(a, nil, nil)
	dispatch := srv.clientSrv.DispatchFunc()
	res, err := dispatch(context.Background(), api.MethodSessionsHistorySessions,
		json.RawMessage(`{"node_id":"home","project_dir":"/p","limit":10,"offset":0}`))
	if err != nil {
		t.Fatalf("historySessions dispatch: %v", err)
	}
	raw, _ := json.Marshal(res)
	var page session.HistorySessionPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode page: %v (%s)", err, raw)
	}
	if len(page.Items) != 1 {
		t.Fatalf("want 1 session, got %d", len(page.Items))
	}
	if got := page.Items[0].NodeID; got != "home" {
		t.Errorf("NodeID = %q, want %q", got, "home")
	}
	if got := page.Items[0].NodeLabel; got != "home-box" {
		t.Errorf("NodeLabel = %q, want %q", got, "home-box")
	}
}
