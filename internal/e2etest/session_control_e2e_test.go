package e2etest

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/gateway"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// isTransportErr reports whether err is a non-RPC transport failure.
// A node-level *api.RPCError means the sealed call reached the handler;
// any other error means the sealed channel itself failed.
func isTransportErr(err error) bool {
	var rpcErr *api.RPCError
	return !errors.As(err, &rpcErr)
}

// TestSessionControlE2E proves that session-addressed control RPCs route over the
// sealed E2E channel to the correct node and arrive with the node-local session id
// (composite id split). Two nodes carry distinct seeded sessions so that wrong-node
// routing and unsplit ids are caught by the "unknown session" error each node returns
// when the session is absent from its own registry.
func TestSessionControlE2E(t *testing.T) {
	agg := gateway.New(time.Second, true)
	srv := gateway.NewServer(agg, nil, nil, true)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two nodes, each with one seeded session. PaneIDs %30/%40 distinguish them from
	// sessions in sibling tests (sr-* tests use %10/%20).
	nA := newE2ENode(t, "sc-node-a", "SC Node A")
	nA.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%30",
		AgentSessionID: "sc-session-a1", Status: session.StatusIdle,
	})
	go nA.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	nB := newE2ENode(t, "sc-node-b", "SC Node B")
	nB.Registry().ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%40",
		AgentSessionID: "sc-session-b1", Status: session.StatusIdle,
	})
	go nB.ConnectGateway(ctx, wsURL(ts.URL, "/node"), "", nil)

	pollConn, err := api.DialWSConn(ctx, wsURL(ts.URL, "/client"), "", nil)
	if err != nil {
		t.Fatalf("poll dial: %v", err)
	}
	poll := api.NewClient(pollConn)
	waitFor(t, "both sc nodes online", func() bool {
		var r api.NodesListResult
		if poll.Call(api.MethodNodesList, nil, &r) != nil {
			return false
		}
		online := 0
		for _, nd := range r.Nodes {
			if (nd.ID == "sc-node-a" || nd.ID == "sc-node-b") &&
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
		t.Fatalf("sessions.list: %v", err)
	}

	compositeIDForNode := func(t *testing.T, nodeID string) string {
		t.Helper()
		for _, s := range allSessions {
			if s.NodeID == nodeID {
				return s.ID
			}
		}
		t.Fatalf("no session for node %s in sessions.list", nodeID)
		return ""
	}

	compA := compositeIDForNode(t, "sc-node-a")
	compB := compositeIDForNode(t, "sc-node-b")

	// Confirm composite ids are distinct and carry the correct node prefix.
	if compA == compB {
		t.Fatalf("sessions from distinct nodes share composite id %q", compA)
	}
	nodeA, localA, okA := session.SplitCompositeID(compA)
	nodeB, localB, okB := session.SplitCompositeID(compB)
	if !okA || !okB {
		t.Fatalf("composite ids are not splittable: %q %q", compA, compB)
	}
	if nodeA != "sc-node-a" || nodeB != "sc-node-b" {
		t.Fatalf("composite id node prefix: got %q/%q, want sc-node-a/sc-node-b", nodeA, nodeB)
	}

	// KEY TEST: split-composite routing proved by sessions.kill.
	//
	// The client splits each composite id (e.g. "sc-node-a:default:%30") to
	// (nodeID="sc-node-a", localID="default:%30") and sends the kill to that node's
	// E2E channel with the node-local id. The routing is proved by what the handler
	// returns:
	//   - If the session is found in the targeted node's registry → the call gets
	//     past the registry lookup and fails at the tmux KillPane level (no real
	//     tmux pane in test). The error does NOT contain "unknown session".
	//   - If the session is not found → the handler returns "unknown session: <id>",
	//     meaning either the wrong node was targeted or the composite id was not split.
	//
	// Conclusion: absence of "unknown session" in the error proves that routing
	// delivered the call to the owning node with the node-local id.
	t.Run("sessions.kill/split-composite-routing", func(t *testing.T) {
		errA := c.Call(api.MethodSessionKill, api.SessionRef{SessionID: compA}, nil)
		if errA == nil {
			// Kill succeeded (e.g. pane happened to exist). Verify removal.
			var after []session.Session
			if err := c.Call(api.MethodSessionsList, nil, &after); err != nil {
				t.Fatalf("follow-up sessions.list: %v", err)
			}
			for _, s := range after {
				if s.ID == compA {
					t.Fatalf("session %q still present after successful kill", compA)
				}
			}
		} else {
			// Kill failed: sealed call reached node A's handler.
			if isTransportErr(errA) {
				t.Fatalf("sessions.kill node-A: transport failure (sealed call did not reach handler): %v", errA)
			}
			// "unknown session" in the error means the session was absent from the targeted
			// node, i.e. routing reached the wrong node or the composite id was not split.
			if strings.Contains(errA.Error(), "unknown session") {
				t.Fatalf("sessions.kill node-A: session %q not found on targeted node (%q=%q unresolved) — wrong-node routing or unsplit composite id: %v",
					compA, "local", localA, errA)
			}
		}

		errB := c.Call(api.MethodSessionKill, api.SessionRef{SessionID: compB}, nil)
		if errB == nil {
			var after []session.Session
			if err := c.Call(api.MethodSessionsList, nil, &after); err != nil {
				t.Fatalf("follow-up sessions.list: %v", err)
			}
			for _, s := range after {
				if s.ID == compB {
					t.Fatalf("session %q still present after successful kill", compB)
				}
			}
		} else {
			if isTransportErr(errB) {
				t.Fatalf("sessions.kill node-B: transport failure: %v", errB)
			}
			if strings.Contains(errB.Error(), "unknown session") {
				t.Fatalf("sessions.kill node-B: session %q not found on targeted node (%q=%q unresolved) — wrong-node routing or unsplit composite id: %v",
					compB, "local", localB, errB)
			}
		}
	})

	// Smoke round-trips for the remaining sessionAddressed control methods.
	// Each subtest invokes one method over the sealed E2E channel and asserts that the
	// sealed round-trip completes — either a valid result or a node-level RPCError —
	// proving the sealed control path reaches the handler. Handler business logic is
	// unit-tested in internal/node; these tests cover only the transport.

	t.Run("sessions.changedFiles", func(t *testing.T) {
		// Session has no Cwd/CurrentPath → handler returns "session working directory unknown".
		// That is a node-level RPCError (not a transport failure), proving the sealed path.
		var result api.ChangedFilesResult
		err := c.Call(api.MethodSessionChangedFiles, api.SessionRef{SessionID: compA}, &result)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.changedFiles: transport failure: %v", err)
		}
	})

	t.Run("sessions.fileDiff", func(t *testing.T) {
		// Supplying a non-empty path; handler returns "session working directory unknown"
		// or "path is required" — both are node-level RPCErrors.
		var result api.FileDiffResult
		err := c.Call(api.MethodSessionFileDiff,
			api.FileDiffParams{SessionID: compA, Path: "README.md"}, &result)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.fileDiff: transport failure: %v", err)
		}
	})

	t.Run("sessions.commits", func(t *testing.T) {
		var result api.CommitsResult
		err := c.Call(api.MethodSessionCommits, api.SessionRef{SessionID: compA}, &result)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.commits: transport failure: %v", err)
		}
	})

	t.Run("sessions.tasks", func(t *testing.T) {
		// Session has no TranscriptPath → handler returns an empty task list (valid result).
		var result api.TasksResult
		err := c.Call(api.MethodSessionTasks, api.SessionRef{SessionID: compA}, &result)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.tasks: transport failure: %v", err)
		}
	})

	t.Run("sessions.respond", func(t *testing.T) {
		// No parked hook → handler logs a warning and returns nil (no error).
		// Success proves the sealed control path completes for respond.
		err := c.Call(api.MethodSessionRespond, api.RespondParams{SessionID: compA, Kind: "option", OptionIndex: 0}, nil)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.respond: transport failure: %v", err)
		}
	})

	t.Run("sessions.input", func(t *testing.T) {
		// Session is controllable (has PaneID) so resolve succeeds; SendText fails
		// at the tmux level (no real pane). That is an RPCError, not a transport failure.
		err := c.Call(api.MethodSessionInput,
			api.InputParams{SessionID: compA, Text: "hello", Submit: false}, nil)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.input: transport failure: %v", err)
		}
	})

	t.Run("sessions.key", func(t *testing.T) {
		err := c.Call(api.MethodSessionKey,
			api.KeyParams{SessionID: compA, Keys: []string{"Escape"}}, nil)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.key: transport failure: %v", err)
		}
	})

	// compositeResult routing: spawn and resume route by node_id (not session_id) and
	// stamp the session_id in the result. Both subtests assert the sealed round-trip
	// reaches handleSessionSpawn/handleSessionResume and returns a node-level RPCError —
	// no real agent is spawned (pty-cap safe).

	// spawn/guard-path proves the compositeResult spawn request was sealed → relayed →
	// dispatched to handleSessionSpawn → sealed rejection returned, without a real spawn.
	// A deliberately nonexistent command guarantees exec.LookPath rejects it regardless
	// of caps.SpawnSession, so no real agent is spawned on any host.
	t.Run("spawn/guard-path", func(t *testing.T) {
		var result api.SpawnResult
		err := c.Call(api.MethodSessionSpawn, api.SpawnParams{NodeID: nodeA, Command: "argus-e2etest-no-such-command"}, &result)
		if err == nil {
			t.Fatal("sessions.spawn: unexpectedly succeeded — real agent was spawned; BLOCKED")
		}
		if isTransportErr(err) {
			t.Fatalf("sessions.spawn: transport failure (sealed call did not reach handler): %v", err)
		}
	})

	// resume/smoke proves compositeResult routing for resume: sealed round-trip reaches
	// handleSessionResume and returns a node-level RPCError (no real session to resume).
	// Empty Cwd hits the "session working directory is unknown" guard deterministically
	// before any spawn attempt, so no real agent is ever launched.
	t.Run("resume/smoke", func(t *testing.T) {
		var result api.ResumeResult
		err := c.Call(api.MethodSessionResume,
			api.ResumeParams{NodeID: nodeA, Agent: "claude", AgentSessionID: "smoke"}, &result)
		if err != nil && isTransportErr(err) {
			t.Fatalf("sessions.resume: transport failure (sealed call did not reach handler): %v", err)
		}
	})
}
