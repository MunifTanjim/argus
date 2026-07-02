package node

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
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
func (d *Node) resolveTranscriptPath(p api.TranscriptSubscribeParams) (path, root string, a adapter.Adapter, err error) {
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session: " + p.SessionID}
	}
	if s.TranscriptPath == "" {
		return "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session has no transcript: " + p.SessionID}
	}
	a = d.adapterFor(s.Agent)
	if p.AgentID == "" {
		return s.TranscriptPath, s.TranscriptPath, a, nil
	}
	sub, ok := a.SubagentFilePath(s.TranscriptPath, p.AgentID)
	if !ok {
		return "", "", nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown subagent: " + p.AgentID}
	}
	return sub, s.TranscriptPath, a, nil
}

// diffChunks returns the first index at which cur differs from old, and whether
// they differ. Folding only changes the tail, so the index is near the end.
func diffChunks(old, cur []transcript.Chunk) (from int, changed bool) {
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

func clampFrom(haveChunks, total int) int {
	from := haveChunks - 1 // resend the possibly-grown last cached chunk
	if from < 0 {
		from = 0
	}
	if from > total {
		from = total
	}
	return from
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
	path, root, a, err := d.resolveTranscriptPath(p)
	if err != nil {
		return nil, err
	}

	st := a.NewStreamingTranscript(path, root, p.AgentID != "")
	chunks, err := st.Refresh()
	if err != nil {
		return nil, err
	}
	from := clampFrom(p.HaveChunks, len(chunks))

	// Start the poller bound to the connection ctx.
	pollCtx, cancel := context.WithCancel(ctx)
	cs.add(p.SubID, cancel)
	go d.pollTranscript(pollCtx, n, p.SubID, st, chunks)

	return api.TranscriptDelta{SubID: p.SubID, FromIndex: from, Chunks: chunks[from:]}, nil
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

// pollTranscript re-folds the transcript every interval and pushes a delta when
// chunks change. `sent` must be the FULL chunk list the client holds (cached
// prefix plus resent tail), not chunks[from:]: diffChunks compares against the
// full fold to compute the from_index. Passing a tail slice would report
// from_index=0 every tick and resend the whole transcript.
func (d *Node) pollTranscript(ctx context.Context, n api.Notifier, subID string, st adapter.StreamingTranscript, sent []transcript.Chunk) {
	defer func() {
		if cs := d.connSubsFor(n); cs != nil {
			cs.remove(subID)
		}
	}()
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
			from, changed := diffChunks(sent, cur)
			if !changed {
				continue
			}
			d.log.Info("transcript.delta", "sub_id", subID, "from", from, "chunks", len(cur)-from, "total", len(cur))
			if err := n.Notify(api.MethodTranscriptDelta, api.TranscriptDelta{
				SubID: subID, FromIndex: from, Chunks: cur[from:],
			}); err != nil {
				return // connection gone
			}
			sent = cur
		}
	}
}
