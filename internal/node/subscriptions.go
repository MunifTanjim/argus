package node

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gitmeta"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// transcriptPollInterval is how often a subscription re-reads its file locally.
const transcriptPollInterval = time.Second

// connSubs holds one connection's active transcript subscriptions.
type connSubs struct {
	mu     sync.Mutex
	cancel map[string]context.CancelFunc // sub_id -> poller cancel
}

func newConnSubs() *connSubs { return &connSubs{cancel: map[string]context.CancelFunc{}} }

func (cs *connSubs) add(subID string, cancel context.CancelFunc) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if old, ok := cs.cancel[subID]; ok {
		old() // replace a stale subscription with the same id
	}
	cs.cancel[subID] = cancel
}

func (cs *connSubs) remove(subID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cancel, ok := cs.cancel[subID]; ok {
		cancel()
		delete(cs.cancel, subID)
	}
}

func (cs *connSubs) closeAll() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for id, cancel := range cs.cancel {
		cancel()
		delete(cs.cancel, id)
	}
}

// registerConn / dropConn track per-connection subscription registries keyed by
// the connection's Notifier (the *Peer). Called from OnConnect.
func (d *Node) registerConn(n api.Notifier) *connSubs {
	cs := newConnSubs()
	d.subsMu.Lock()
	d.conns[n] = cs
	d.subsMu.Unlock()
	return cs
}

func (d *Node) dropConn(n api.Notifier) {
	d.subsMu.Lock()
	cs := d.conns[n]
	delete(d.conns, n)
	d.subsMu.Unlock()
	if cs != nil {
		cs.closeAll()
	}
}

func (d *Node) connSubsFor(n api.Notifier) *connSubs {
	d.subsMu.Lock()
	defer d.subsMu.Unlock()
	return d.conns[n]
}

// getOrCreateConnSubs returns the per-connection registry for n, creating it on
// first use. Direct clients are pre-registered in OnConnect; connections reaching
// handlers another way (gateway uplink, in-process gateway) are registered lazily
// here and dropped when their context ends.
func (d *Node) getOrCreateConnSubs(ctx context.Context, n api.Notifier) *connSubs {
	d.subsMu.Lock()
	cs, ok := d.conns[n]
	if !ok {
		cs = newConnSubs()
		d.conns[n] = cs
	}
	d.subsMu.Unlock()
	if !ok {
		go func() { <-ctx.Done(); d.dropConn(n) }()
	}
	return cs
}

// resolveTranscriptPath returns the file to tail for a subscription: the session
// transcript, or a subagent file when AgentID is set.
func (d *Node) resolveTranscriptPath(p api.TranscriptSubscribeParams) (path, root, cwd string, a adapter.Adapter, err error) {
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return "", "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session: " + p.SessionID}
	}
	if s.TranscriptPath == "" {
		return "", "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session has no transcript: " + p.SessionID}
	}
	a = d.adapterFor(s.Agent)
	if p.AgentID == "" {
		return s.TranscriptPath, s.TranscriptPath, s.Cwd, a, nil
	}
	sub, ok := a.SubagentFilePath(s.TranscriptPath, p.AgentID)
	if !ok {
		return "", "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown subagent: " + p.AgentID}
	}
	return sub, s.TranscriptPath, s.Cwd, a, nil
}

// diffEntries returns the first index at which cur differs from old, and whether
// they differ. Folding only changes the tail, so the index is near the end.
func diffEntries(old, cur []transcript.Entry) (from int, changed bool) {
	if len(old) == len(cur) && (len(cur) == 0 || &old[0] == &cur[0]) {
		return 0, false // an unchanged fold returns the same slice
	}
	n := len(old)
	if len(cur) < n {
		n = len(cur)
	}
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(old[i], cur[i]) {
			return i, true
		}
	}
	if len(cur) != len(old) {
		return n, true
	}
	return 0, false
}

// clampFrom picks the index to resend from on (re)subscribe. Entries of the
// open turn can mutate after they were first sent (a tool result lands, a spawn
// resolves its subagent), so it resends the whole turn that holds the client's
// last cached entry. A user entry is not a boundary: a prompt queued while the
// agent is busy lands inside the open turn.
func clampFrom(entries []transcript.Entry, haveEntries int) int {
	last := haveEntries - 1
	if last >= len(entries) {
		last = len(entries) - 1
	}
	if last < 0 {
		return 0
	}
	if turnBoundary(entries[last].Kind) {
		return last
	}
	for i := last - 1; i >= 0; i-- {
		if turnBoundary(entries[i].Kind) {
			return i + 1
		}
	}
	return 0
}

func turnBoundary(k transcript.EntryKind) bool {
	switch k {
	case transcript.EntryTurnEnd, transcript.EntrySystem, transcript.EntryCompact,
		transcript.EntryShell:
		return true
	}
	return false
}

func (d *Node) handleTranscriptSubscribe(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TranscriptSubscribeParams](params)
	if err != nil {
		return nil, err
	}
	if p.SubID == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "sub_id required"}
	}
	n, ok := api.NotifierFrom(ctx)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no connection notifier"}
	}
	cs := d.getOrCreateConnSubs(ctx, n)
	path, root, cwd, a, err := d.resolveTranscriptPath(p)
	if err != nil {
		return nil, err
	}

	// Opening a session is a reliable moment to refresh its git branch: a
	// mid-session checkout fires no agent hook, so we recompute from cwd here.
	// Subagent opens (AgentID set) share the parent cwd, so skip them.
	if p.AgentID == "" && cwd != "" {
		d.reg.SetBranch(p.SessionID, gitmeta.Branch(cwd))
	}

	st := a.NewStreamingTranscript(path, root, p.AgentID != "")
	entries, err := st.Refresh()
	if err != nil {
		return nil, err
	}
	from := clampFrom(entries, p.HaveEntries)

	// Task-change detection rides this subscription: only for the main session
	// (not subagent views), and only when the agent persists a task list. It
	// reuses the entries the poller already folds each tick — no extra I/O.
	var taskSignals func([]transcript.Entry) (int, bool)
	if p.AgentID == "" {
		if ts, ok := a.(adapter.TaskSource); ok {
			taskSignals = ts.TaskActivityCount
		}
	}

	// Interrupt-aware idle surfacing rides the main-session poll: an interrupted
	// turn fires no Stop hook, so the poll re-derives idle from the folded
	// transcript and surfaces the compose prompt. Subagent views never drive status.
	driveStatus := p.AgentID == ""

	// Start the poller bound to the connection ctx.
	pollCtx, cancel := context.WithCancel(ctx)
	cs.add(p.SubID, cancel)
	go d.pollTranscript(pollCtx, n, p.SubID, p.SessionID, st, taskSignals, entries, driveStatus)

	return api.TranscriptDelta{SubID: p.SubID, FromIndex: from, Entries: entries[from:]}, nil
}

func (d *Node) handleTranscriptUnsubscribe(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TranscriptUnsubscribeParams](params)
	if err != nil {
		return nil, err
	}
	if n, ok := api.NotifierFrom(ctx); ok {
		if cs := d.connSubsFor(n); cs != nil {
			cs.remove(p.SubID)
		}
	}
	return nil, nil
}

// surfaceIdleAfterInterrupt surfaces the idle composer when the folded transcript's
// last turn ended by an interrupt (which fires no Stop hook). It handles both a
// stranded prompt (interrupted in the user's terminal while argus showed a
// question/permission) and a turn the parked-decision cleanup already reset to
// working. The observed interaction kind drives SurfaceIdle's compare-and-swap.
func (d *Node) surfaceIdleAfterInterrupt(sessionID string) {
	s, ok := d.reg.Get(sessionID)
	if !ok {
		return
	}
	var expect session.InteractionKind
	if s.Interaction != nil {
		expect = s.Interaction.Kind
	}
	d.reg.SurfaceIdle(sessionID, expect)
}

// pollTranscript re-folds the transcript every interval and pushes a delta when
// entries change. `sent` must be the FULL entry list the client holds (cached
// prefix plus resent tail), not entries[from:]: diffEntries compares against the
// full fold to compute the from_index. Passing a tail slice would report
// from_index=0 every tick and resend the whole transcript.
func (d *Node) pollTranscript(ctx context.Context, n api.Notifier, subID, sessionID string, st adapter.StreamingTranscript, taskSignals func([]transcript.Entry) (int, bool), sent []transcript.Entry, driveStatus bool) {
	defer func() {
		if cs := d.connSubsFor(n); cs != nil {
			cs.remove(subID)
		}
	}()
	var lastSignals int
	if taskSignals != nil {
		lastSignals, _ = taskSignals(sent) // seed from catch-up; don't fire on open
	}
	var idleSurfaced bool
	t := time.NewTicker(transcriptPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur, err := st.Refresh()
			if err != nil {
				continue // transient (file rotated/locked); try next tick
			}
			if driveStatus {
				interrupted := lastTurnInterrupted(cur)
				if interrupted && !idleSurfaced {
					d.surfaceIdleAfterInterrupt(sessionID)
				}
				idleSurfaced = interrupted
			}
			if taskSignals != nil {
				// Count only grows; resync the baseline on a shrink (transcript
				// rotation), but push only on a rise — and only once a task tool
				// has appeared (hasTool). Without one, a teammate message is just
				// chatter from a task-less team; those tasks are caught on open /
				// manual refresh.
				if c, hasTool := taskSignals(cur); c != lastSignals {
					rose := c > lastSignals
					lastSignals = c
					if rose && hasTool {
						if err := n.Notify(api.MethodTasksChanged, api.TasksChanged{SubID: subID, SessionID: sessionID}); err != nil {
							return // connection gone
						}
					}
				}
			}
			from, changed := diffEntries(sent, cur)
			if !changed {
				continue
			}
			d.log.Info("transcript.delta", "sub_id", subID, "from", from, "entries", len(cur)-from, "total", len(cur))
			if err := n.Notify(api.MethodTranscriptDelta, api.TranscriptDelta{
				SubID: subID, FromIndex: from, Entries: cur[from:],
			}); err != nil {
				return // connection gone
			}
			sent = cur
		}
	}
}

func lastTurnInterrupted(entries []transcript.Entry) bool {
	if len(entries) == 0 {
		return false
	}
	last := entries[len(entries)-1]
	return last.Kind == transcript.EntryTurnEnd && last.Interrupted
}
