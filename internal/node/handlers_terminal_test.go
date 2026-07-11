package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// recordingNotifier is a test double that routes terminal.output notifications
// to a typed channel and all other notifications to a generic channel.
type recordingNotifier struct {
	outputs chan api.TerminalOutput
	other   chan api.Notification
}

func newRecordingNotifier() *recordingNotifier {
	return &recordingNotifier{
		outputs: make(chan api.TerminalOutput, 32),
		other:   make(chan api.Notification, 32),
	}
}

func (r *recordingNotifier) Notify(method string, params any) error {
	if method == api.MethodTerminalOutput {
		raw, _ := json.Marshal(params)
		var out api.TerminalOutput
		_ = json.Unmarshal(raw, &out)
		select {
		case r.outputs <- out:
		default:
		}
		return nil
	}
	raw, _ := json.Marshal(params)
	select {
	case r.other <- api.Notification{Method: method, Params: raw}:
	default:
	}
	return nil
}

// seedSession seeds a session with the given tmux pane into d.reg and returns
// the generated session ID.
func seedSession(t *testing.T, d *Node, loc session.TmuxLocation) string {
	t.Helper()
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Server: loc.Server,
		PaneID: loc.PaneID,
	})
	return s.ID
}

// TestTerminalOpenStreamsAndCloses opens a terminal on a real tmux session,
// asserts that at least one terminal.output notification arrives, then closes.
func TestTerminalOpenStreamsAndCloses(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"

	sessionID := seedSession(t, d, session.TmuxLocation{
		Server: session.TmuxServerDefault, PaneID: pane,
		SessionName: "origin", WindowIndex: 0,
	})

	notif := newRecordingNotifier()
	ctx = api.WithNotifier(ctx, notif)

	_, err = d.handleTerminalOpen(ctx, mustJSON(api.TerminalOpenParams{
		TermID: "t1", SessionID: sessionID, Cols: 80, Rows: 24,
	}))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case m := <-notif.outputs:
		if m.TermID != "t1" || m.Data == "" {
			t.Fatalf("bad output: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no terminal.output received")
	}

	if _, err := d.handleTerminalClose(ctx, mustJSON(api.TerminalCloseParams{TermID: "t1"})); err != nil {
		t.Fatal(err)
	}
}

// TestTerminalOpenEvictsExistingViewer enforces one viewer per session: a second
// open (on a different connection) boots the first with terminal.exited{evicted},
// then owns the per-session index. Pipes stand in for PTYs so eviction is asserted
// without depending on a real tmux mirror staying alive between the two opens.
func TestTerminalOpenEvictsExistingViewer(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.restoreMirrorFn = func(*tmux.Client, *mirrorState) {}

	// Viewer A on connection ctA, registered as the sole viewer of session "s".
	nA := newRecordingNotifier()
	rA, wA, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer wA.Close()
	_, cancelA := context.WithCancel(context.Background())
	ctA := newConnTerms()
	tmA := &term{pty: rA, cancel: cancelA, sessionID: "s", termID: "tA", notifier: nA, ct: ctA}
	ctA.m["tA"] = tmA
	d.sessionTerms["s"] = tmA
	go d.pumpTerm(tmA)

	// A second open evicts A before the new viewer registers (as handleTerminalOpen does).
	d.evictSessionTerm("s")
	if !waitEvicted(nA, "tA", 3*time.Second) {
		t.Fatal("viewer A was not evicted")
	}

	// Viewer B on connection ctB registers as the new sole viewer.
	nB := newRecordingNotifier()
	rB, wB, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer wB.Close()
	_, cancelB := context.WithCancel(context.Background())
	ctB := newConnTerms()
	tmB := &term{pty: rB, cancel: cancelB, sessionID: "s", termID: "tB", notifier: nB, ct: ctB}
	ctB.m["tB"] = tmB
	d.sessionTerms["s"] = tmB
	go d.pumpTerm(tmB)

	// The session now maps to B's attach only.
	d.sessionTermsMu.Lock()
	cur := d.sessionTerms["s"]
	d.sessionTermsMu.Unlock()
	if cur == nil || cur.termID != "tB" {
		t.Fatalf("sessionTerms should be owned by tB, got %+v", cur)
	}

	// B streams what its pty produces.
	if _, err := wB.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-nB.outputs:
		if m.TermID != "tB" {
			t.Fatalf("bad output for B: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("viewer B: no output")
	}
}

// TestSessionEndBootsViewer verifies a live attach is booted when its session ends:
// the registry removal drives the same boot as eviction, with reason{exited}.
func TestSessionEndBootsViewer(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"

	sessionID := seedSession(t, d, session.TmuxLocation{
		Server: session.TmuxServerDefault, PaneID: pane,
		SessionName: "origin", WindowIndex: 0,
	})

	n := newRecordingNotifier()
	ctxN := api.WithNotifier(ctx, n)
	if _, err := d.handleTerminalOpen(ctxN, mustJSON(api.TerminalOpenParams{
		TermID: "t1", SessionID: sessionID, Cols: 80, Rows: 24,
	})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-n.outputs:
	case <-time.After(3 * time.Second):
		t.Fatal("no output before session end")
	}

	// Subscribe before removing so the watch can't miss the event.
	events, unsub := d.reg.Subscribe()
	defer unsub()
	go d.watchSessionExitsLoop(ctx, events)

	// Agent exits: the session goes dead, publishing the removal the watch reacts to.
	d.reg.ApplyHook(registry.HookUpdate{
		Server: session.TmuxServerDefault, PaneID: pane, Status: session.StatusDead,
	})

	if !waitExited(n, "t1", 3*time.Second) {
		t.Fatal("viewer was not booted on session end")
	}
	d.sessionTermsMu.Lock()
	cur := d.sessionTerms[sessionID]
	d.sessionTermsMu.Unlock()
	if cur != nil {
		t.Fatalf("sessionTerms should be empty after session end, got %+v", cur)
	}
}

// TestTeardownTermIsIdempotent verifies teardownTerm runs exactly once even when
// called twice (eviction + client close racing). The guard stops a double restore.
func TestTeardownTermIsIdempotent(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	var restores int32
	d.restoreMirrorFn = func(*tmux.Client, *mirrorState) { atomic.AddInt32(&restores, 1) }

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, cancel := context.WithCancel(context.Background())
	tm := &term{pty: r, cancel: cancel, sessionID: "s", termID: "t", ct: newConnTerms()}

	d.teardownTerm(tm)
	d.teardownTerm(tm) // second teardown must be a no-op

	if got := atomic.LoadInt32(&restores); got != 1 {
		t.Fatalf("restoreMirror called %d times, want 1", got)
	}
}

// TestConcurrentOpenSameSessionKeepsSingleViewer fires several opens for one
// session at once and asserts exactly one attach is live. Without serialized
// evict→setup→register, concurrent openers all register.
func TestConcurrentOpenSameSessionKeepsSingleViewer(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	pane, err := c.NewSession(ctx, tmux.NewSessionOpts{Name: "origin", Command: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	d := newNode(map[session.TmuxServer]*tmux.Client{session.TmuxServerDefault: c})
	d.mirrorPrefix, d.mirrorSuffix = "_", "_"
	sessionID := seedSession(t, d, session.TmuxLocation{
		Server: session.TmuxServerDefault, PaneID: pane,
		SessionName: "origin", WindowIndex: 0,
	})

	const n = 3
	notifs := make([]*recordingNotifier, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		notifs[i] = newRecordingNotifier()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release all opens together to maximize the race
			octx := api.WithNotifier(ctx, notifs[i])
			_, _ = d.handleTerminalOpen(octx, mustJSON(api.TerminalOpenParams{
				TermID: fmt.Sprintf("t%d", i), SessionID: sessionID, Cols: 80, Rows: 24,
			}))
		}(i)
	}
	close(start)
	wg.Wait()

	// Exactly one attach may be live across all per-connection registries.
	live := 0
	d.termsMu.Lock()
	for _, ct := range d.terms {
		ct.mu.Lock()
		live += len(ct.m)
		ct.mu.Unlock()
	}
	d.termsMu.Unlock()
	if live != 1 {
		t.Fatalf("want exactly 1 live viewer after %d concurrent opens, got %d", n, live)
	}
	d.sessionTermsMu.Lock()
	cur := d.sessionTerms[sessionID]
	d.sessionTermsMu.Unlock()
	if cur == nil {
		t.Fatal("no surviving viewer in the per-session index")
	}

	// Cleanup: close whichever term survived (others are already torn down).
	for i := 0; i < n; i++ {
		octx := api.WithNotifier(ctx, notifs[i])
		_, _ = d.handleTerminalClose(octx, mustJSON(api.TerminalCloseParams{TermID: fmt.Sprintf("t%d", i)}))
	}
}

// waitEvicted drains n's non-output notifications until it sees a
// terminal.exited{termID, evicted}, or the deadline elapses.
func waitEvicted(n *recordingNotifier, termID string, d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case msg := <-n.other:
			if msg.Method != api.MethodTerminalExited {
				continue
			}
			var ex api.TerminalExited
			if json.Unmarshal(msg.Params, &ex) != nil {
				continue
			}
			if ex.TermID == termID && ex.Reason == api.TermExitedEvicted {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// waitExited waits for a terminal.exited{exited} for termID, ignoring any other
// notification (e.g. the pump's own reasonless exit as the PTY closes).
func waitExited(n *recordingNotifier, termID string, d time.Duration) bool {
	deadline := time.After(d)
	for {
		select {
		case msg := <-n.other:
			if msg.Method != api.MethodTerminalExited {
				continue
			}
			var ex api.TerminalExited
			if json.Unmarshal(msg.Params, &ex) != nil {
				continue
			}
			if ex.TermID == termID && ex.Reason == api.TermExitedProcess {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// blockingNotifier blocks in Notify until release is closed, standing in for a
// viewer whose connection has stopped draining its socket.
type blockingNotifier struct{ release chan struct{} }

func (b *blockingNotifier) Notify(string, any) error {
	<-b.release
	return nil
}

// TestBootSessionTermDoesNotBlockOnStuckViewer verifies boot sends terminal.exited
// off its critical path: a stuck evicted viewer must not block bootSessionTerm
// (which runs under openMu).
func TestBootSessionTermDoesNotBlockOnStuckViewer(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.restoreMirrorFn = func(*tmux.Client, *mirrorState) {}

	bn := &blockingNotifier{release: make(chan struct{})}
	defer close(bn.release) // let the async notify goroutine finish

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, cancel := context.WithCancel(context.Background())
	ct := newConnTerms()
	tm := &term{pty: r, cancel: cancel, sessionID: "s", termID: "t", notifier: bn, ct: ct}
	ct.m["t"] = tm
	d.sessionTerms["s"] = tm

	done := make(chan struct{})
	go func() { d.bootSessionTerm("s", api.TermExitedEvicted); close(done) }()

	select {
	case <-done:
		// good: boot returned without waiting for the stuck notify
	case <-time.After(2 * time.Second):
		t.Fatal("bootSessionTerm blocked on a stuck viewer's Notify")
	}
}

// TestEvictedViewerGetsOnlyEvictedReason verifies the eviction path reports a
// single, correctly-reasoned exit: eviction deletes the term from ct.m before
// closing the pty, so the pump's now-failing read finds it no longer owns the
// term and must not emit a second exited{process} past the boot's evicted.
func TestEvictedViewerGetsOnlyEvictedReason(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.restoreMirrorFn = func(*tmux.Client, *mirrorState) {}

	n := newRecordingNotifier()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	_, cancel := context.WithCancel(context.Background())
	ct := newConnTerms()
	tm := &term{pty: r, cancel: cancel, sessionID: "s", termID: "t", notifier: n, ct: ct}
	ct.m["t"] = tm
	d.sessionTerms["s"] = tm

	// Pump blocks reading the pipe; eviction closes it, failing that read.
	go d.pumpTerm(tm)

	d.bootSessionTerm("s", api.TermExitedEvicted)

	// Collect every terminal.exited over a window: exactly one, evicted.
	reasons := collectExitReasons(n, "t", time.Second)
	if len(reasons) != 1 || reasons[0] != api.TermExitedEvicted {
		t.Fatalf("exit reasons = %v, want exactly [%s]", reasons, api.TermExitedEvicted)
	}
}

// collectExitReasons drains n's non-output notifications for the given window,
// returning the Reason of every terminal.exited seen for termID (empty reasons
// normalized to TermExitedProcess, matching the wire contract).
func collectExitReasons(n *recordingNotifier, termID string, window time.Duration) []string {
	var reasons []string
	deadline := time.After(window)
	for {
		select {
		case msg := <-n.other:
			if msg.Method != api.MethodTerminalExited {
				continue
			}
			var ex api.TerminalExited
			if json.Unmarshal(msg.Params, &ex) != nil || ex.TermID != termID {
				continue
			}
			r := ex.Reason
			if r == "" {
				r = api.TermExitedProcess
			}
			reasons = append(reasons, r)
		case <-deadline:
			return reasons
		}
	}
}

func TestClampSize(t *testing.T) {
	cases := []struct {
		name                         string
		cols, rows, wantCol, wantRow int
	}{
		{"non-positive defaults", 0, -5, defaultCols, defaultRows},
		{"in range untouched", 120, 40, 120, 40},
		{"upper bound capped", 70000, 70000, maxTermCols, maxTermRows},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotCol, gotRow := clampSize(c.cols, c.rows)
			if gotCol != c.wantCol || gotRow != c.wantRow {
				t.Fatalf("clampSize(%d,%d) = (%d,%d), want (%d,%d)",
					c.cols, c.rows, gotCol, gotRow, c.wantCol, c.wantRow)
			}
		})
	}
}
