package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/tmux"
)

const termOutputChunk = 32 * 1024

const (
	defaultCols = 80
	defaultRows = 24
	// Upper bounds so a bogus size can't silently wrap when narrowed to uint16
	// for the PTY winsize. Far above any real terminal.
	maxTermCols = 1000
	maxTermRows = 1000
)

// clampSize applies defaults for non-positive dims and caps the upper bound.
func clampSize(cols, rows int) (int, int) {
	switch {
	case cols <= 0:
		cols = defaultCols
	case cols > maxTermCols:
		cols = maxTermCols
	}
	switch {
	case rows <= 0:
		rows = defaultRows
	case rows > maxTermRows:
		rows = maxTermRows
	}
	return cols, rows
}

// term is one live PTY attach.
type term struct {
	pty    *os.File
	mirror *mirrorState
	client *tmux.Client
	cancel context.CancelFunc

	// Identity needed to boot this attach from anywhere (single-viewer eviction).
	sessionID string
	termID    string
	notifier  api.Notifier // the connection to notify on eviction
	ct        *connTerms   // the per-connection registry this term lives in

	// teardownOnce guards teardownTerm: eviction, client close, pump exit, and
	// connection drop can all reach the same term, but it must be torn down once.
	teardownOnce sync.Once
}

// connTerms holds one connection's live terminals, keyed by term_id.
type connTerms struct {
	mu sync.Mutex
	m  map[string]*term
}

func newConnTerms() *connTerms { return &connTerms{m: map[string]*term{}} }

// termsFor returns (creating if needed) the per-connection terminal registry for
// n, wiring first-time creation to drop all terminals when the connection ends.
func (d *Node) termsFor(ctx context.Context, n api.Notifier) *connTerms {
	d.termsMu.Lock()
	ct, ok := d.terms[n]
	if !ok {
		ct = newConnTerms()
		d.terms[n] = ct
	}
	d.termsMu.Unlock()
	if !ok {
		go func() { <-ctx.Done(); d.dropConnTerms(n) }()
	}
	return ct
}

// connTermsFor returns the existing terminal registry for the calling
// connection, or ok=false if there is none.
func (d *Node) connTermsFor(ctx context.Context) (*connTerms, bool) {
	n, ok := api.NotifierFrom(ctx)
	if !ok {
		return nil, false
	}
	d.termsMu.Lock()
	ct := d.terms[n]
	d.termsMu.Unlock()
	return ct, ct != nil
}

// dropConnTerms removes n's terminal registry and tears down all live terminals.
func (d *Node) dropConnTerms(n api.Notifier) {
	d.termsMu.Lock()
	ct := d.terms[n]
	delete(d.terms, n)
	d.termsMu.Unlock()
	if ct == nil {
		return
	}
	ct.mu.Lock()
	snapshot := ct.m
	ct.m = map[string]*term{}
	ct.mu.Unlock()
	for _, tm := range snapshot {
		d.teardownTerm(tm)
	}
}

// teardownTerm cancels the attach context, closes the PTY, and restores the
// mirror. Callers must not hold ct.mu: restoreMirror does blocking tmux round-trips.
func (d *Node) teardownTerm(tm *term) {
	tm.teardownOnce.Do(func() {
		d.forgetSessionTerm(tm)
		tm.cancel()
		_ = tm.pty.Close()
		d.restoreMirrorFn(tm.client, tm.mirror)
	})
}

// forgetSessionTerm drops tm from the per-session index, but only if it is still
// the current entry — so a newer attach that already replaced it is left intact.
func (d *Node) forgetSessionTerm(tm *term) {
	d.sessionTermsMu.Lock()
	if d.sessionTerms[tm.sessionID] == tm {
		delete(d.sessionTerms, tm.sessionID)
	}
	d.sessionTermsMu.Unlock()
}

// bootSessionTerm removes the live attach for sessionID (if any), notifies the
// viewer via terminal.exited{reason}, and tears it down. Reports whether a term
// was booted. Guarded removals let it race safely with a concurrent close/pump.
func (d *Node) bootSessionTerm(sessionID, reason string) bool {
	d.sessionTermsMu.Lock()
	tm := d.sessionTerms[sessionID]
	if tm != nil {
		delete(d.sessionTerms, sessionID)
	}
	d.sessionTermsMu.Unlock()
	if tm == nil {
		return false
	}
	tm.ct.mu.Lock()
	if tm.ct.m[tm.termID] == tm {
		delete(tm.ct.m, tm.termID)
	}
	tm.ct.mu.Unlock()
	// Notify off the critical path: eviction runs under openMu, so a stuck viewer
	// must not block every other terminal.open. Teardown stays serialized.
	go func(n api.Notifier, termID string) {
		_ = n.Notify(api.MethodTerminalExited, api.TerminalExited{TermID: termID, Reason: reason})
	}(tm.notifier, tm.termID)
	d.teardownTerm(tm)
	return true
}

// evictSessionTerm enforces one viewer per session: it boots any existing attach
// for sessionID (from this or another connection) before a new one is created.
func (d *Node) evictSessionTerm(sessionID string) {
	d.bootSessionTerm(sessionID, api.TermExitedEvicted)
}

// watchSessionExits boots any live terminal whose session ends (registry removal),
// so a viewer never lingers on the shell the agent leaves behind. An externally-
// killed agent is out of scope — no removal fires until a later scan.
func (d *Node) watchSessionExits(ctx context.Context) {
	events, cancel := d.reg.Subscribe()
	defer cancel()
	d.watchSessionExitsLoop(ctx, events)
}

// watchSessionExitsLoop is the watch loop split out for testing against a
// caller-supplied event channel.
func (d *Node) watchSessionExitsLoop(ctx context.Context, events <-chan registry.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == registry.EventRemoved {
				// Boot off the loop's critical path: bootSessionTerm does blocking
				// tmux round-trips, and a burst of removals must not stall this
				// consumer. Concurrent boots are safe (per-term teardownOnce).
				go d.bootSessionTerm(ev.Session.ID, api.TermExitedProcess)
			}
		}
	}
}

func (d *Node) handleTerminalOpen(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalOpenParams](params)
	if err != nil {
		return nil, err
	}
	if p.TermID == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "term_id required"}
	}
	n, ok := api.NotifierFrom(ctx)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInternalError, Message: "no connection notifier"}
	}
	s, c, err := d.resolve(p.SessionID)
	if err != nil {
		return nil, err
	}
	// A mirror grouped onto the caller's own tmux window fights that client over the
	// window's size and zoom (visible glitching), so refuse that case. ClientPane must
	// name a pane on this session's tmux server (clientPaneFor scopes it); the
	// comparison is meaningless across servers.
	if p.ClientPane != "" {
		if same, err := c.PanesShareWindow(ctx, p.ClientPane, s.Tmux.PaneID); err != nil {
			return nil, err
		} else if same {
			return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session shares your tmux window; use the pane directly"}
		}
	}
	cols, rows := clampSize(p.Cols, p.Rows)
	ct := d.termsFor(ctx, n)
	// Serialize the whole open so evict→setup→register is atomic: two concurrent
	// opens of the same session must not both observe no prior viewer.
	d.openMu.Lock()
	defer d.openMu.Unlock()
	// Single viewer per session: boot any existing attach before creating the new
	// one. Evicting first also lets the new mirror capture a clean origin baseline.
	// Tradeoff: if setupMirror/startPTY below then fail, the old viewer is already
	// gone and the new one never comes up — accepted, as two mirrors can't safely
	// overlap on the shared origin window and setup failure here is rare.
	d.evictSessionTerm(s.ID)

	m, err := d.setupMirror(ctx, c, s, p.TermID)
	if err != nil {
		return nil, err
	}
	// Detach the attach process from the request context; it lives until close.
	attachCtx, cancel := context.WithCancel(context.Background())
	f, err := startPTY(c.AttachCommand(attachCtx, m.name), cols, rows)
	if err != nil {
		cancel()
		d.restoreMirror(c, m)
		return nil, err
	}
	tm := &term{
		pty: f, mirror: m, client: c, cancel: cancel,
		sessionID: s.ID, termID: p.TermID, notifier: n, ct: ct,
	}
	ct.mu.Lock()
	prior := ct.m[p.TermID]
	ct.m[p.TermID] = tm
	ct.mu.Unlock()
	if prior != nil {
		// A pre-existing entry means a reused term_id; tear it down (outside ct.mu,
		// as teardownTerm requires) so its goroutine + mirror don't leak.
		d.teardownTerm(prior)
	}
	d.sessionTermsMu.Lock()
	d.sessionTerms[s.ID] = tm
	d.sessionTermsMu.Unlock()

	go d.pumpTerm(tm)
	return nil, nil
}

// pumpTerm streams PTY output to the client until EOF, a read error, or a failed
// Notify, then drops itself (guarded so a concurrent close/replace wins) and tears
// down. terminal.exited{process} is sent only when this pump still owns the term,
// so a read that failed because the term was evicted elsewhere can't race a second,
// wrongly-reasoned exit past the boot's terminal.exited{evicted}.
func (d *Node) pumpTerm(tm *term) {
	ptyEnded := false
	defer func() {
		tm.ct.mu.Lock()
		owned := tm.ct.m[tm.termID] == tm // guard: don't tear down a term replaced/closed elsewhere
		if owned {
			delete(tm.ct.m, tm.termID)
		}
		tm.ct.mu.Unlock()
		if !owned {
			return // evicted/closed/replaced elsewhere: that path owns teardown + notify
		}
		if ptyEnded {
			// The shell exited on its own; tell the viewer so it can leave.
			_ = tm.notifier.Notify(api.MethodTerminalExited, api.TerminalExited{TermID: tm.termID})
		}
		d.teardownTerm(tm)
	}()
	buf := make([]byte, termOutputChunk)
	for {
		nr, err := tm.pty.Read(buf)
		if nr > 0 {
			data := base64.StdEncoding.EncodeToString(buf[:nr])
			if e := tm.notifier.Notify(api.MethodTerminalOutput, api.TerminalOutput{TermID: tm.termID, Data: data}); e != nil {
				return // connection gone; defer handles teardown
			}
		}
		if err != nil {
			if err != io.EOF {
				d.log.Info("terminal.output read", "term_id", tm.termID, "err", err)
			}
			ptyEnded = true
			return
		}
	}
}

func (d *Node) handleTerminalInput(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalInputParams](params)
	if err != nil {
		return nil, err
	}
	tm, ok := d.lookupTerm(ctx, p.TermID)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown term_id: " + p.TermID}
	}
	raw, err := base64.StdEncoding.DecodeString(p.Data)
	if err != nil {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "bad base64"}
	}
	_, err = tm.pty.Write(raw)
	return nil, err
}

func (d *Node) handleTerminalResize(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalResizeParams](params)
	if err != nil {
		return nil, err
	}
	tm, ok := d.lookupTerm(ctx, p.TermID)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown term_id: " + p.TermID}
	}
	if p.Cols > 0 && p.Rows > 0 {
		// Resize the PTY client only; the shared window follows via window-size
		// latest, which also resizes the origin — accepted.
		cols, rows := clampSize(p.Cols, p.Rows)
		_ = resizePTY(tm.pty, cols, rows)
	}
	return nil, nil
}

func (d *Node) handleTerminalClose(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.TerminalCloseParams](params)
	if err != nil {
		return nil, err
	}
	ct, ok := d.connTermsFor(ctx)
	if !ok {
		return nil, nil
	}
	ct.mu.Lock()
	tm := ct.m[p.TermID]
	delete(ct.m, p.TermID)
	ct.mu.Unlock()
	if tm != nil {
		d.teardownTerm(tm)
	}
	return nil, nil
}

// lookupTerm returns the live terminal for termID on the calling connection.
func (d *Node) lookupTerm(ctx context.Context, termID string) (*term, bool) {
	ct, ok := d.connTermsFor(ctx)
	if !ok {
		return nil, false
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()
	tm, ok := ct.m[termID]
	return tm, ok
}
