package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/session"
)

// modDecisionLease ends a mod permission request nobody has waited on for this
// long: the hook is gone (answered or cancelled in Claude, Claude exited, the mod
// reloaded). A var so tests can shorten it.
var modDecisionLease = 5 * time.Second

// modPermission is a permission request a mod registered and waits on in
// decision requests.
type modPermission struct {
	id             string
	agentSessionID string
	toolName       string
	toolInput      json.RawMessage
	out            chan string // buffered 1: the decision output, "" when none
	cancel         context.CancelFunc
	lease          *time.Timer
	open           int // decision waits in flight; guarded by Node.modPermsMu
}

// startModPermission parks a permission request for the user's answer in argus
// without blocking the caller, and returns the id the mod waits on.
func (d *Node) startModPermission(a adapter.Adapter, s session.Session, ev adapter.HookEvent) string {
	toolName, toolInput := a.PermissionPayload(ev)
	ctx, cancel := context.WithCancel(context.Background())
	p := &modPermission{
		id:             newModRequestID(),
		agentSessionID: s.AgentSessionID,
		toolName:       toolName,
		toolInput:      toolInput,
		out:            make(chan string, 1),
		cancel:         cancel,
	}
	// Armed only after p.lease is assigned: the callback reads it.
	p.lease = time.AfterFunc(time.Hour, func() { d.endModPermission(p) })
	p.lease.Reset(modDecisionLease)
	d.modPermsMu.Lock()
	d.modPerms[p.id] = p
	d.modPermsMu.Unlock()
	go func() {
		// awaitDecision clears the prompt on every exit; a cancelled ctx ends it empty.
		p.out <- d.awaitDecision(ctx, a, s.ID, ev)
		cancel()
		// An answer given between waits gets a full lease for the mod to collect it.
		d.modPermsMu.Lock()
		if p.open == 0 && d.modPerms[p.id] == p {
			p.lease.Reset(modDecisionLease)
		}
		d.modPermsMu.Unlock()
	}()
	return p.id
}

// waitModPermission waits up to modHold for the request's answer. done
// is false when the hold ran out; an unknown or ended request is done and empty.
// The lease is held off while the wait is open.
func (d *Node) waitModPermission(ctx context.Context, id string) (output string, done bool) {
	d.modPermsMu.Lock()
	p := d.modPerms[id]
	if p != nil {
		if p.open++; p.open == 1 {
			p.lease.Stop()
		}
	}
	d.modPermsMu.Unlock()
	if p == nil {
		return "", true
	}
	hold := time.NewTimer(modHold)
	defer hold.Stop()
	select {
	case out := <-p.out:
		if ctx.Err() == nil {
			d.modPermsMu.Lock()
			p.open--
			d.modPermsMu.Unlock()
			d.forgetModPermission(p)
			return out, true
		}
		// The mod hung up and cannot take the answer: keep it for its next wait.
		p.out <- out
	case <-hold.C:
	case <-ctx.Done():
	}
	d.modPermsMu.Lock()
	if p.open--; p.open == 0 {
		p.lease.Reset(modDecisionLease)
	}
	d.modPermsMu.Unlock()
	return "", false
}

// endModPermission ends a request whatever its state: the prompt clears in argus
// and any open wait answers done and empty.
func (d *Node) endModPermission(p *modPermission) {
	p.lease.Stop()
	p.cancel()
	d.forgetModPermission(p)
}

func (d *Node) forgetModPermission(p *modPermission) {
	d.modPermsMu.Lock()
	if d.modPerms[p.id] == p {
		delete(d.modPerms, p.id)
	}
	d.modPermsMu.Unlock()
}

func (d *Node) cancelModPermission(id string) {
	d.modPermsMu.Lock()
	p := d.modPerms[id]
	d.modPermsMu.Unlock()
	if p != nil {
		d.endModPermission(p)
	}
}

// endModPermissionsRan ends the session's requests for the tool call a
// PostToolUse reports: the user allowed it in Claude, which tells the mod nothing.
func (d *Node) endModPermissionsRan(payload json.RawMessage) {
	d.modPermsMu.Lock()
	none := len(d.modPerms) == 0
	d.modPermsMu.Unlock()
	if none {
		return
	}
	var ran struct {
		SessionID string          `json:"session_id"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if json.Unmarshal(payload, &ran) != nil || ran.SessionID == "" {
		return
	}
	d.modPermsMu.Lock()
	var ended []*modPermission
	for _, p := range d.modPerms {
		if p.agentSessionID == ran.SessionID && p.toolName == ran.ToolName &&
			(oneAtATime[p.toolName] || containsJSON(ran.ToolInput, p.toolInput)) {
			ended = append(ended, p)
		}
	}
	d.modPermsMu.Unlock()
	for _, p := range ended {
		d.endModPermission(p)
	}
}

// oneAtATime lists the interactive tools Claude shows one at a time. Their
// PostToolUse input differs from the request's (answers added, or the plan
// dropped), so the tool name alone identifies the call.
var oneAtATime = map[string]bool{"AskUserQuestion": true, "ExitPlanMode": true}

// containsJSON reports whether every field of the object want appears in the
// object got with an equal value; got may carry more, as a tool's recorded
// input can.
func containsJSON(got, want json.RawMessage) bool {
	var g, w map[string]any
	if json.Unmarshal(got, &g) != nil || json.Unmarshal(want, &w) != nil {
		return false
	}
	for k, v := range w {
		if gv, ok := g[k]; !ok || !reflect.DeepEqual(gv, v) {
			return false
		}
	}
	return true
}

func newModRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
