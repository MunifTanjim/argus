package pi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/gittree"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// sessionIdleTTL bounds how long a tracked foreign session unseen (no
// heartbeat, event, or connection) stays listed.
const sessionIdleTTL = time.Hour

// foreignSession is a pi session Argus did not spawn (typically an interactive
// TUI session) whose globally-installed gate extension dialed the beacon
// socket. Control flows as intents over that connection; status and approvals
// flow back the same way. Dismiss only untracks the card — the user's process
// is never touched.
type foreignSession struct {
	id    string
	cwd   string
	file  string
	name  string
	model string
	pid   int

	conn net.Conn
	wmu  chan struct{} // send serialization (buffered 1 as mutex)

	mu           sync.Mutex
	streaming    bool
	lastSeen     time.Time
	pending      map[string]*pendingUI
	disconnected bool
}

// defaultSocketPath is the well-known beacon address, mirroring the gate
// extension's default: ARGUS_SOCKET wins (unix path, or host:port for TCP
// where unix sockets are unavailable), else the runtime dir, else the temp
// dir keyed by uid.
func defaultSocketPath() string {
	if p := os.Getenv("ARGUS_SOCKET"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "argus-pi.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("argus-pi-%d.sock", os.Getuid()))
}

// socketEndpoint splits an address into network + listen/dial target: an
// explicit host:port uses TCP, everything else a unix socket.
func socketEndpoint(addr string) (network, target string) {
	if host, port, err := net.SplitHostPort(addr); err == nil && host != "" && port != "" {
		if _, err := strconv.Atoi(port); err == nil {
			return "tcp", addr
		}
	}
	return "unix", addr
}

// ensureBeacon binds the beacon socket once. A live socket owned by another
// Argus node is left alone (its beacons stay with that node); a stale file is
// replaced. Failures are silent: the next ScanOnce retries.
func (d *discoverer) ensureBeacon() {
	d.blnMu.Lock()
	defer d.blnMu.Unlock()
	if d.bln != nil {
		return
	}
	addr := d.sockPath()
	network, target := socketEndpoint(addr)
	if network == "unix" {
		if probe, err := net.DialTimeout("unix", target, 200*time.Millisecond); err == nil {
			probe.Close()
			return // another node serves it
		}
		_ = os.Remove(target)
	}
	ln, err := net.Listen(network, target)
	if err != nil {
		return
	}
	d.bln = ln
	d.blnAddr = addr
	go d.beaconAccept(ln)
}

func (d *discoverer) beaconAccept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go d.serveBeacon(conn)
	}
}

// serveBeacon speaks the beacon protocol (JSONL, LF-framed like RPC) with one
// gate extension until it disconnects, which drops the foreign card.
func (d *discoverer) serveBeacon(conn net.Conn) {
	defer conn.Close()
	fs := &foreignSession{pending: map[string]*pendingUI{}, lastSeen: time.Now()}
	fs.conn = conn
	fs.wmu = make(chan struct{}, 1)
	var id string
	defer func() {
		if id != "" {
			d.forgetForeign(id)
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
		switch probe.Type {
		case "hello":
			id = d.helloForeign(fs, line)
			if id == "" {
				return // passive ack for an owned session; hold the conn, ignore rest
			}
		case "event":
			if id != "" {
				d.foreignEvent(id, line)
			}
		case "approval_request":
			if id != "" {
				d.foreignApproval(id, line)
			}
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
	fs.wmu <- struct{}{}
	defer func() { <-fs.wmu }()
	_ = fs.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = fs.conn.Write(line)
	return err
}

type beaconHello struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	SessionFile string `json:"sessionFile"`
	Cwd         string `json:"cwd"`
	Model       string `json:"model"`
	Idle        bool   `json:"idle"`
	PID         int    `json:"pid"`
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
	fs.mu.Lock()
	fs.id, fs.cwd, fs.file, fs.model, fs.pid = h.SessionID, h.Cwd, h.SessionFile, h.Model, h.PID
	fs.lastSeen = time.Now()
	fs.mu.Unlock()
	d.mu.Lock()
	if old := d.foreign[h.SessionID]; old != nil {
		old.mu.Lock()
		old.disconnected = true
		old.mu.Unlock()
		_ = old.conn.Close()
	}
	if d.foreign == nil {
		d.foreign = map[string]*foreignSession{}
	}
	d.foreign[h.SessionID] = fs
	d.mu.Unlock()
	_ = d.beaconSend(fs, map[string]any{"type": "hello_ack", "passive": false})
	st := session.StatusWorking
	ix := (*session.Interaction)(nil)
	if h.Idle {
		st = session.StatusAwaitingInput
		ix = &session.Interaction{Kind: session.InteractionIdle}
	}
	d.foreignCard(fs, st, ix, true)
	return h.SessionID
}

func (d *discoverer) lookupForeign(id string) *foreignSession {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.foreign[id]
}

// foreignCard pushes a foreign session's card. TranscriptPath may be empty:
// pi creates the file only after the first user/assistant message.
func (d *discoverer) foreignCard(fs *foreignSession, st session.Status, ix *session.Interaction, replace bool) {
	fs.mu.Lock()
	fs.lastSeen = time.Now()
	u := registry.HookUpdate{
		Agent:              Agent,
		AgentSessionID:     fs.id,
		Name:               fs.name,
		Cwd:                fs.cwd,
		Repo:               gittree.RepoName(fs.cwd),
		TranscriptPath:     fs.file,
		Frontend:           session.FrontendExternal,
		Input:              session.InputAPI,
		Status:             st,
		Interaction:        ix,
		ReplaceInteraction: replace,
	}
	fs.mu.Unlock()
	d.reg.ApplyHook(u)
}

// forgetForeign drops a foreign card on disconnect or sweep. It reports
// whether the id was tracked.
func (d *discoverer) forgetForeign(id string) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	fs := d.foreign[id]
	if fs == nil {
		d.mu.Unlock()
		return false
	}
	delete(d.foreign, id)
	d.mu.Unlock()
	d.reg.ApplyHook(registry.HookUpdate{Agent: Agent, AgentSessionID: id, Status: session.StatusDead})
	return true
}

type beaconEvent struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
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
	hasPending := len(fs.pending) > 0
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
	}
}

type beaconApproval struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	ToolName string `json:"toolName"`
	Detail   string `json:"detail"`
}

// foreignApproval parks a beacon approval request as an Argus permission
// interaction, answered later by foreignRespond.
func (d *discoverer) foreignApproval(id string, line []byte) {
	fs := d.lookupForeign(id)
	if fs == nil {
		return
	}
	// The extension posts {type approval_request, id, toolName, detail}; map
	// through the shared dialog parser via the confirm shape the gate uses.
	var a beaconApproval
	if json.Unmarshal(line, &a) != nil || a.ID == "" {
		return
	}
	raw, _ := json.Marshal(uiRequest{
		Type: "extension_ui_request", ID: a.ID, Method: "confirm",
		Title: "Allow " + a.ToolName + "?", Message: strings.TrimSpace(a.ToolName + " " + a.Detail),
	})
	pend, ix, ok := parseUIRequest(raw)
	if !ok {
		return
	}
	fs.mu.Lock()
	fs.pending[pend.uiID] = pend
	fs.mu.Unlock()
	d.foreignCard(fs, session.StatusAwaitingInput, ix, true)
}

// sweepForeign drops foreign sessions unseen past the idle TTL.
func (d *discoverer) sweepForeign() {
	cutoff := time.Now().Add(-sessionIdleTTL)
	d.mu.Lock()
	var stale []string
	for id, fs := range d.foreign {
		fs.mu.Lock()
		stale_ := fs.lastSeen.Before(cutoff)
		fs.mu.Unlock()
		if stale_ {
			stale = append(stale, id)
		}
	}
	d.mu.Unlock()
	for _, id := range stale {
		d.forgetForeign(id)
	}
}
