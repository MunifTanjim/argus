package node

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// awaitDecision must clear the session's pending interaction on every exit — when
// the user answers in argus, when the hook goes away (ctx cancel: dismissed in
// Claude), and on timeout — so a stale prompt never lingers.
func TestAwaitDecisionClearsOnAllExits(t *testing.T) {
	ev := claudecode.HookEvent{
		Event:   "PermissionRequest",
		Payload: json.RawMessage(`{"tool_name":"AskUserQuestion","tool_input":{}}`),
	}

	setup := func() (*Node, string) {
		d := newNode(map[session.TmuxServer]*tmux.Client{})
		s, _ := d.reg.ApplyHook(registry.HookUpdate{
			Tool: "claude-code", Server: session.TmuxServerDefault, PaneID: "%9",
			Status: session.StatusAwaitingInput,
			Interaction: &session.Interaction{
				Kind:      session.InteractionQuestion,
				Questions: []session.QuestionSpec{{Question: "Q", Options: []string{"A"}}},
			},
		})
		return d, s.ID
	}
	waitParked := func(d *Node, id string) {
		t.Helper()
		for i := 0; i < 200; i++ {
			d.pendingMu.Lock()
			_, ok := d.pending[id]
			d.pendingMu.Unlock()
			if ok {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("decision was never parked")
	}
	cleared := func(d *Node, id string) bool {
		s, _ := d.reg.Get(id)
		return s.Interaction == nil
	}

	// 1. Hook goes away (ctx cancelled): clears, returns "".
	d, id := setup()
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan string, 1)
	go func() { out <- d.awaitDecision(ctx, d.adapterFor(""), id, ev) }()
	waitParked(d, id)
	cancel()
	if got := <-out; got != "" {
		t.Errorf("ctx cancel: out=%q want empty", got)
	}
	if !cleared(d, id) {
		t.Error("ctx cancel: interaction not cleared")
	}

	// 2. Timeout: clears, returns "".
	d, id = setup()
	old := decisionTimeout
	decisionTimeout = 10 * time.Millisecond
	got := d.awaitDecision(context.Background(), d.adapterFor(""), id, ev)
	decisionTimeout = old
	if got != "" {
		t.Errorf("timeout: out=%q want empty", got)
	}
	if !cleared(d, id) {
		t.Error("timeout: interaction not cleared")
	}

	// 3. Answered in argus: returns the decision and clears.
	d, id = setup()
	out = make(chan string, 1)
	go func() { out <- d.awaitDecision(context.Background(), d.adapterFor(""), id, ev) }()
	waitParked(d, id)
	d.pendingMu.Lock()
	pd := d.pending[id]
	d.pendingMu.Unlock()
	pd.ch <- "DECISION"
	if got := <-out; got != "DECISION" {
		t.Errorf("answered: out=%q want DECISION", got)
	}
	if !cleared(d, id) {
		t.Error("answered: interaction not cleared")
	}
}
