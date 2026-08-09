package e2etest

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// The transcript must live under ~/.claude/projects/ to satisfy ReadHistoryTranscript's
// path restriction.
func writeTempProjectsTranscript(t *testing.T) string {
	t.Helper()
	// The in-process node resolves the projects root via os.UserHomeDir, so isolating
	// HOME redirects both this write and the node's read to a throwaway dir.
	t.Setenv("HOME", t.TempDir())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("skipping historyTranscript: cannot get home dir: %v", err)
	}
	dir := filepath.Join(home, ".claude", "projects", "argus-e2etest")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skipf("skipping historyTranscript: cannot create projects dir: %v", err)
	}
	f, err := os.CreateTemp(dir, "transcript-*.jsonl")
	if err != nil {
		t.Skipf("skipping historyTranscript: cannot create transcript: %v", err)
	}
	content := `{"type":"user","uuid":"u1","timestamp":"2026-06-12T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-06-12T10:00:01Z","message":{"role":"assistant","model":"claude-opus-4-5","stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":10},"content":[{"type":"text","text":"hi there"}]}}
`
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Skipf("skipping historyTranscript: cannot write transcript: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Skipf("skipping historyTranscript: cannot close transcript: %v", err)
	}
	path := f.Name()
	t.Cleanup(func() {
		os.Remove(path)
		os.Remove(dir)
	})
	return path
}

func newE2ENode(t *testing.T, id, label string) *node.Node {
	t.Helper()
	n := node.New()
	n.SetIdentity(id, label)
	n.SetVersion("itest")
	kp, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair for %s: %v", id, err)
	}
	n.SetIdentityKey(kp)
	n.SetE2EE(true)
	return n
}

// Two nodes are used so sessions.list merge + per-node composite stamping is covered.
func TestSessionReadE2E(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n1 := newE2ENode(t, "sr-node-a", "Node A")
	n1.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%10",
		AgentSessionID: "sr-session-a1", Status: session.StatusIdle,
	})
	go n1.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	n2 := newE2ENode(t, "sr-node-b", "Node B")
	n2.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%20",
		AgentSessionID: "sr-session-b1", Status: session.StatusIdle,
	})
	go n2.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "both nodes online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		online := 0
		for _, nd := range r.Nodes {
			if (nd.ID == "sr-node-a" || nd.ID == "sr-node-b") &&
				nd.IdentityPubKey != "" && nd.Online {
				online++
			}
		}
		return online == 2
	})
	poll.Close()

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()

	var allSessions []session.Session
	if err := c.Call(api.MethodSessionsList, nil, &allSessions); err != nil {
		t.Fatalf("initial sessions.list: %v", err)
	}

	t.Run("sessions.list/merge-and-stamp", func(t *testing.T) {
		if len(allSessions) < 2 {
			t.Fatalf("sessions.list returned %d sessions, want >=2", len(allSessions))
		}
		byNode := map[string]session.Session{}
		for _, s := range allSessions {
			nodeID, _, ok := session.SplitCompositeID(s.ID)
			if !ok {
				t.Fatalf("session id %q is not composite", s.ID)
			}
			if nodeID != s.NodeID {
				t.Fatalf("composite node prefix %q != NodeID field %q", nodeID, s.NodeID)
			}
			byNode[s.NodeID] = s
		}
		for _, want := range []string{"sr-node-a", "sr-node-b"} {
			if _, ok := byNode[want]; !ok {
				t.Fatalf("no session from node %s in list", want)
			}
		}
		if byNode["sr-node-a"].ID == byNode["sr-node-b"].ID {
			t.Fatalf("sessions from distinct nodes share id %q", byNode["sr-node-a"].ID)
		}
	})

	compositeIDForNode := func(t *testing.T, nodeID string) string {
		t.Helper()
		for _, s := range allSessions {
			if s.NodeID == nodeID {
				return s.ID
			}
		}
		t.Fatalf("no session for node %s in initial list", nodeID)
		return ""
	}

	t.Run("sessions.transcriptView", func(t *testing.T) {
		compositeID := compositeIDForNode(t, "sr-node-a")
		var view transcript.TranscriptView
		if err := c.Call(api.MethodSessionTranscriptView,
			api.SessionRef{SessionID: compositeID}, &view); err != nil {
			t.Fatalf("sessions.transcriptView: %v", err)
		}
		// Empty view is valid (no transcript path seeded); the sealed round-trip succeeded.
	})

	t.Run("sessions.historyProjects", func(t *testing.T) {
		var projects []session.HistoryProject
		if err := c.Call(api.MethodSessionsHistoryProjects, nil, &projects); err != nil {
			t.Fatalf("sessions.historyProjects: %v", err)
		}
		// Empty-but-valid; proves the sealed fanout round-trip.
	})

	t.Run("sessions.historySessions", func(t *testing.T) {
		var page session.HistorySessionPage
		if err := c.Call(api.MethodSessionsHistorySessions,
			api.HistorySessionsParams{NodeID: "sr-node-a"}, &page); err != nil {
			t.Fatalf("sessions.historySessions: %v", err)
		}
		// Empty-but-valid; proves the sealed node-addressed round-trip.
	})

	t.Run("sessions.historyTranscript", func(t *testing.T) {
		tmp := writeTempProjectsTranscript(t)
		var view transcript.TranscriptView
		if err := c.Call(api.MethodSessionsHistoryTranscript,
			api.HistoryTranscriptParams{
				NodeID:         "sr-node-a",
				Agent:          "claude",
				TranscriptPath: tmp,
			}, &view); err != nil {
			t.Fatalf("sessions.historyTranscript: %v", err)
		}
		if len(view.Chunks) == 0 {
			t.Fatal("historyTranscript over e2e: expected entries from the seeded transcript, got empty view")
		}
	})
}

// Exercises the SealNotificationFrame path (node) → forwardFromNode relay → client
// onRelayFrame notification decode.
func TestSessionEventOverE2E(t *testing.T) {
	agg := gateway.New(time.Second)
	srv := gateway.NewServer(agg, nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n := newE2ENode(t, "ev-node", "Event Node")
	go n.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "ev-node online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		for _, nd := range r.Nodes {
			if nd.ID == "ev-node" && nd.IdentityPubKey != "" && nd.Online {
				return true
			}
		}
		return false
	})
	poll.Close()

	dial := func(ctx context.Context) (net.Conn, error) {
		return api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	}
	c, err := client.NewReconnectingE2EClient(ctx, dial)
	if err != nil {
		t.Fatalf("NewReconnectingE2EClient: %v", err)
	}
	defer c.Close()

	// NewReconnectingE2EClient completes Connect() before returning, so the E2E
	// channel is already up and streamRegistry has sent its (empty) snapshot.
	n.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%99",
		AgentSessionID: "ev-session-1", Status: session.StatusIdle,
	})

	select {
	case ev := <-c.Events():
		if ev.Method != api.MethodSessionEvent {
			t.Fatalf("got event method %q, want %q", ev.Method, api.MethodSessionEvent)
		}
		var regEv registry.Event
		if err := json.Unmarshal(ev.Params, &regEv); err != nil {
			t.Fatalf("unmarshal session.event params: %v", err)
		}
		nodeID, _, ok := session.SplitCompositeID(regEv.Session.ID)
		if !ok {
			t.Fatalf("session id %q is not composite", regEv.Session.ID)
		}
		if nodeID != "ev-node" {
			t.Fatalf("event session node prefix %q, want %q", nodeID, "ev-node")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for session.event on Events()")
	}
}
