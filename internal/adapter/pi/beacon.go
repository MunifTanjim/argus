package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// paneLookupTimeout bounds the ps and tmux calls a beacon hello waits on.
const paneLookupTimeout = 2 * time.Second

// sessionIdleTTL bounds how long a tracked foreign session unseen (no
// heartbeat, event, or connection) stays listed.
const sessionIdleTTL = time.Hour

// switchGrace is how long a disconnected beacon's card waits for a hello from
// the same process that names it as previousSessionId.
var switchGrace = 2 * time.Second

// foreignSession is a pi session Argus did not spawn (typically an interactive
// TUI session) whose globally-installed gate extension opened a beacon
// connection on the mod socket. Control flows as intents over that connection;
// status and dialogs flow back the same way. Dismiss only untracks the card —
// the user's process is never touched.
// Its card queue holds the prompts Argus sent that pi has not taken yet.
type foreignSession struct {
	card

	conn net.Conn
	wmu  sync.Mutex

	// Guarded by card.mu.
	pid          int
	server       session.TmuxServer
	paneID       string
	lastSeen     time.Time
	shellWaits   map[string]chan beaconShellReady // intent_shell id -> its answer
	dialogs      []string                         // titles of other extensions' dialogs open in pi
	disconnected bool
}

// serveBeacon speaks the beacon protocol (JSONL, LF-framed like RPC) with one
// gate extension until it disconnects, which drops the foreign card.
func (d *discoverer) serveBeacon(conn net.Conn) {
	defer conn.Close()
	fs := &foreignSession{card: card{pending: map[string]*pendingUI{}}, conn: conn, lastSeen: time.Now()}
	grace := switchGrace
	var id string
	defer func() {
		if id != "" {
			fs.mu.Lock()
			fs.disconnected = true
			fs.mu.Unlock()
			// A session switch reloads the gate, which reconnects and names this
			// session in its hello; ending the card at once would drop it.
			time.AfterFunc(grace, func() { d.forgetForeign(id, fs) })
		}
	}()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if probe.Type != "hello" && id == "" {
			continue
		}
		switch probe.Type {
		case "hello":
			if id = d.helloForeign(fs, line); id == "" {
				return // passive: drop the connection
			}
		case "event":
			d.foreignEvent(id, line)
		case "ui_request":
			d.foreignUIRequest(id, line)
		case "ui_settled":
			d.foreignUISettled(id, line)
		case "shell_ready":
			d.foreignShellReady(id, line)
		case "heartbeat":
			if fs := d.lookupForeign(id); fs != nil {
				fs.mu.Lock()
				fs.lastSeen = time.Now()
				fs.mu.Unlock()
			}
		}
	}
}

// beaconSend writes one record to a foreign session, serialized per connection.
func (d *discoverer) beaconSend(fs *foreignSession, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	fs.wmu.Lock()
	defer fs.wmu.Unlock()
	_ = fs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = fs.conn.Write(line)
	return err
}

type beaconHello struct {
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
	Cwd         string `json:"cwd"`
	Model       string `json:"model"`
	Reasoning   bool   `json:"reasoning"`
	Level       string `json:"thinkingLevel"`
	Idle        bool   `json:"idle"`
	PID         int    `json:"pid"`
	// PreviousSessionID is the session the same process left for this one.
	PreviousSessionID string `json:"previousSessionId"`
}

// helloForeign registers a beacon client. Sessions Argus owns get a passive
// ack (managed mode should have disabled the beacon, but the ack is the
// backstop against double control). Returns the tracked id, or "" when passive.
func (d *discoverer) helloForeign(fs *foreignSession, line []byte) string {
	var h beaconHello
	if json.Unmarshal(line, &h) != nil || h.SessionID == "" {
		return ""
	}
	if d.get(h.SessionID) != nil {
		_ = d.beaconSend(fs, map[string]any{"type": "hello_ack", "passive": true})
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), paneLookupTimeout)
	server, paneID := d.paneFor(ctx, h.PID)
	cancel()
	repo := gittree.RepoName(h.Cwd)
	fs.mu.Lock()
	fs.id, fs.cwd, fs.repo, fs.file, fs.pid = h.SessionID, h.Cwd, repo, h.SessionFile, h.PID
	fs.model, fs.reasoning, fs.level = h.Model, h.Reasoning, h.Level
	fs.lastSeen = time.Now()
	fs.mu.Unlock()
	d.mu.Lock()
	var prev *foreignSession
	if h.PreviousSessionID != "" && h.PreviousSessionID != h.SessionID {
		// Taken over without Dead; its pending forget then finds another entry.
		if prev = d.foreign[h.PreviousSessionID]; prev != nil {
			delete(d.foreign, h.PreviousSessionID)
			fs.mu.Lock()
			fs.prevID = h.PreviousSessionID
			fs.mu.Unlock()
		}
	}
	var switched map[string]*foreignSession
	if paneID != "" {
		server, paneID, switched = d.claimPaneLocked(h.PID, h.SessionID, server, paneID)
	}
	fs.mu.Lock()
	fs.server, fs.paneID = server, paneID
	fs.mu.Unlock()
	old := d.foreign[h.SessionID]
	d.foreign[h.SessionID] = fs
	d.mu.Unlock()
	if old != nil {
		_ = old.conn.Close()
	}
	if prev != nil {
		_ = prev.conn.Close()
	}
	for id, other := range switched {
		d.forgetForeign(id, other)
	}
	if paneID != "" && d.paneHeldByRegistry(h.SessionID, h.PreviousSessionID, server, paneID) {
		fs.mu.Lock()
		fs.server, fs.paneID = "", ""
		fs.mu.Unlock()
	}
	_ = d.beaconSend(fs, map[string]any{"type": "hello_ack", "passive": false})
	fs.mu.Lock()
	fs.streaming = !h.Idle
	fs.mu.Unlock()
	st := session.StatusWorking
	ix := (*session.Interaction)(nil)
	if h.Idle {
		st = session.StatusAwaitingInput
		ix = &session.Interaction{Kind: session.InteractionIdle}
	}
	d.foreignCard(fs, st, ix, true)
	return h.SessionID
}

// paneHeldByRegistry reports whether a card other than this session's (or the
// one it switched from) holds the pane. A switched card keeps its first record
// id, so the card is matched by agent session id.
func (d *discoverer) paneHeldByRegistry(sessionID, prevID string, server session.TmuxServer, paneID string) bool {
	for _, s := range d.reg.Snapshot() {
		own := s.Agent == Agent && s.AgentSessionID != "" && (s.AgentSessionID == sessionID || s.AgentSessionID == prevID)
		if !own && s.Tmux.Server == server && s.Tmux.PaneID == paneID {
			return true
		}
	}
	return false
}

// claimPaneLocked binds a beacon to the tmux pane its process runs in. A child
// process inherits its parent's tty, so a pane another live process holds
// stays with it; the same pi process switching sessions, or any process after
// the holder disconnected, takes it over, and the entries it leaves behind are
// returned for the caller to forget. Callers hold
// d.mu so concurrent hellos cannot both claim one pane.
func (d *discoverer) claimPaneLocked(pid int, sessionID string, server session.TmuxServer, paneID string) (session.TmuxServer, string, map[string]*foreignSession) {
	switched := map[string]*foreignSession{}
	for id, other := range d.foreign {
		if id == sessionID {
			continue
		}
		other.mu.Lock()
		held := other.server == server && other.paneID == paneID
		releasable := other.pid == pid || other.disconnected
		other.mu.Unlock()
		if !held {
			continue
		}
		if !releasable {
			return "", "", nil
		}
		switched[id] = other
	}
	return server, paneID, switched
}

func (d *discoverer) lookupForeign(id string) *foreignSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.foreign[id]
}

func (d *discoverer) foreignCard(fs *foreignSession, st session.Status, ix *session.Interaction, replace bool) {
	fs.mu.Lock()
	fs.lastSeen = time.Now()
	u := fs.hookUpdate(st, ix, replace)
	u.Server, u.PaneID = fs.server, fs.paneID
	fs.mu.Unlock()
	d.reg.ApplyHook(u)
	if u.PrevAgentSessionID != "" {
		fs.mu.Lock()
		if fs.prevID == u.PrevAgentSessionID {
			fs.prevID = ""
		}
		fs.mu.Unlock()
	}
}

// forgetForeign drops a foreign card on disconnect or sweep. It removes the
// entry only if it is still fs, so a replaced connection cannot drop its
// successor; a nil fs matches whatever entry is there. It reports whether an
// entry was removed.
func (d *discoverer) forgetForeign(id string, want *foreignSession) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	fs := d.foreign[id]
	if fs == nil || (want != nil && fs != want) {
		d.mu.Unlock()
		return false
	}
	delete(d.foreign, id)
	d.mu.Unlock()
	fs.mu.Lock()
	server, paneID := fs.server, fs.paneID
	fs.mu.Unlock()
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Server: server, PaneID: paneID, Status: session.StatusDead})
	return true
}

type beaconEvent struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Model       string   `json:"model"`
	Reasoning   bool     `json:"reasoning"`
	Level       string   `json:"thinkingLevel"`
	Items       []string `json:"items"`
	Open        bool     `json:"open"`
	Title       string   `json:"title"`
}

func (d *discoverer) foreignEvent(id string, line []byte) {
	fs := d.lookupForeign(id)
	if fs == nil {
		return
	}
	var ev beaconEvent
	if json.Unmarshal(line, &ev) != nil {
		return
	}
	fs.mu.Lock()
	hasPending := len(fs.pending) > 0 || len(fs.dialogs) > 0
	fs.mu.Unlock()
	switch ev.Name {
	case "agent_start":
		fs.mu.Lock()
		fs.streaming = true
		fs.lastSeen = time.Now()
		fs.mu.Unlock()
		if hasPending {
			return
		}
		d.foreignCard(fs, session.StatusWorking, nil, false)
	case "agent_settled":
		fs.mu.Lock()
		fs.streaming = false
		fs.lastSeen = time.Now()
		fs.mu.Unlock()
		if hasPending {
			return
		}
		d.foreignCard(fs, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	case "session_info_changed":
		if ev.DisplayName == "" {
			return
		}
		fs.mu.Lock()
		fs.name = ev.DisplayName
		fs.mu.Unlock()
		d.foreignCard(fs, "", nil, false)
	case "model":
		fs.mu.Lock()
		fs.model, fs.reasoning, fs.level = ev.Model, ev.Reasoning, ev.Level
		fs.mu.Unlock()
		d.foreignCard(fs, "", nil, false)
	case "queue":
		fs.mu.Lock()
		fs.queue = ev.Items
		fs.mu.Unlock()
	case "compaction":
		if !ev.Open {
			d.foreignSettle(fs)
			return
		}
		if hasPending {
			return
		}
		d.foreignCard(fs, session.StatusWorking, nil, false)
	case "ui_prompt":
		fs.mu.Lock()
		if ev.Open {
			fs.dialogs = append(fs.dialogs, ev.Title)
		} else if i := slices.Index(fs.dialogs, ev.Title); i >= 0 {
			fs.dialogs = slices.Delete(fs.dialogs, i, i+1)
		}
		fs.mu.Unlock()
		d.foreignSettle(fs)
	}
}

// foreignSettle shows a foreign session's state once no dialog waits: a
// dialog open in pi, else working or idle.
func (d *discoverer) foreignSettle(fs *foreignSession) {
	fs.mu.Lock()
	dialogs, streaming := len(fs.pending), fs.streaming
	var dialog string
	open := len(fs.dialogs) > 0
	if open {
		dialog = fs.dialogs[len(fs.dialogs)-1]
	}
	fs.mu.Unlock()
	switch {
	case dialogs > 0:
	case open:
		msg := "Waiting on a dialog in pi"
		if dialog != "" {
			msg = "Waiting in pi: " + dialog
		}
		d.foreignCard(fs, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle, Message: msg}, true)
	case streaming:
		d.foreignCard(fs, session.StatusWorking, nil, true)
	default:
		d.foreignCard(fs, session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle}, true)
	}
}

// foreignUIRequest parks a dialog the gate forwarded from pi as an Argus
// interaction, answered later by respondForeign.
func (d *discoverer) foreignUIRequest(id string, line []byte) {
	fs := d.lookupForeign(id)
	if fs == nil {
		return
	}
	var req uiRequest
	if json.Unmarshal(line, &req) != nil || req.ID == "" {
		return
	}
	if req.Method != "select" && req.Method != "confirm" && req.Method != "input" {
		return
	}
	pend, ix, ok := uiInteraction(req)
	if !ok {
		return
	}
	fs.mu.Lock()
	fs.pending[pend.uiID] = pend
	fs.mu.Unlock()
	d.foreignCard(fs, session.StatusAwaitingInput, ix, true)
}

// foreignUISettled takes down a dialog answered in the pi terminal.
func (d *discoverer) foreignUISettled(id string, line []byte) {
	fs := d.lookupForeign(id)
	if fs == nil {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(line, &req) != nil || req.ID == "" {
		return
	}
	d.dropForeignPending(fs, req.ID)
}

// shellReadyTimeout bounds the wait for the gate to stage a "!" command. A gate
// loaded before Argus could stage one never answers.
var shellReadyTimeout = 2 * time.Second

type beaconShellReady struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// stageForeign stages text ("!cmd", /new) as the user's own input in a foreign
// session: the gate sets it in pi's editor, keeping the user's draft aside, for
// Enter in the session's pane to submit. pi offers extensions no way to submit
// it themselves. A session without a pane fails with noPane.
func (d *discoverer) stageForeign(ctx context.Context, fs *foreignSession, text string, noPane error) error {
	reqID := d.nextID()
	ready := make(chan beaconShellReady, 1)
	fs.mu.Lock()
	if fs.paneID == "" {
		fs.mu.Unlock()
		return fmt.Errorf("pi: %w", noPane)
	}
	if len(fs.dialogs) > 0 {
		fs.mu.Unlock()
		return fmt.Errorf("pi: %w", api.ErrSessionBusy)
	}
	if fs.shellWaits == nil {
		fs.shellWaits = map[string]chan beaconShellReady{}
	}
	fs.shellWaits[reqID] = ready
	fs.mu.Unlock()
	defer func() {
		fs.mu.Lock()
		delete(fs.shellWaits, reqID)
		fs.mu.Unlock()
	}()
	if err := d.beaconSend(fs, map[string]any{"type": "intent_shell", "id": reqID, "text": text}); err != nil {
		return err
	}
	select {
	case r := <-ready:
		if !r.OK {
			return fmt.Errorf("pi: %s", r.Reason)
		}
	case <-time.After(shellReadyTimeout):
		return errors.New("pi: the session did not take the command; reload it (/reload) to load the current Argus gate")
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (d *discoverer) foreignShellReady(id string, line []byte) {
	fs := d.lookupForeign(id)
	if fs == nil {
		return
	}
	var r beaconShellReady
	if json.Unmarshal(line, &r) != nil {
		return
	}
	fs.mu.Lock()
	ready := fs.shellWaits[r.ID]
	fs.mu.Unlock()
	if ready != nil {
		select {
		case ready <- r:
		default:
		}
	}
}

// sweepForeign drops foreign sessions unseen past the idle TTL.
func (d *discoverer) sweepForeign() {
	cutoff := time.Now().Add(-sessionIdleTTL)
	d.mu.Lock()
	var stale []string
	for id, fs := range d.foreign {
		fs.mu.Lock()
		if fs.lastSeen.Before(cutoff) {
			stale = append(stale, id)
		}
		fs.mu.Unlock()
	}
	d.mu.Unlock()
	for _, id := range stale {
		d.forgetForeign(id, nil)
	}
}
