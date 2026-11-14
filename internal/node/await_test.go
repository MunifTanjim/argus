package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

func bashPermission(agentID string) claudecode.HookEvent {
	raw, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": "ls"}, "agent_id": agentID})
	return claudecode.HookEvent{Event: "PermissionRequest", Payload: raw}
}

func awaitNode(t *testing.T) (*Node, string) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%9",
		Status: session.StatusWorking,
	})
	return d, s.ID
}

type parkedCall struct {
	cancel context.CancelFunc
	out    chan string
}

// parkRequest runs awaitDecision for id in the background and waits until
// its prompt is queued on the session.
func parkRequest(t *testing.T, d *Node, sid, id string, ev claudecode.HookEvent) parkedCall {
	t.Helper()
	events, unsubscribe := d.reg.Subscribe()
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := parkedCall{cancel: cancel, out: make(chan string, 1)}
	go func() { c.out <- d.awaitDecision(ctx, d.adapterFor(""), sid, id, ev) }()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Session.ID == sid && overlaid(d, sid) {
				if !pendingHas(d, id) {
					t.Fatalf("%s queued but not parked", id)
				}
				return c
			}
		case <-timeout:
			t.Fatalf("%s was never queued", id)
		}
	}
}

func pendingHas(d *Node, id string) bool {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	return d.pending[id] != nil
}

func head(d *Node, sid string) *session.Interaction {
	s, _ := d.reg.Get(sid)
	return s.Interaction
}

func respond(t *testing.T, d *Node, p api.RespondParams) error {
	t.Helper()
	raw, _ := json.Marshal(p)
	_, err := d.handleSessionRespond(context.Background(), raw)
	return err
}

func TestAwaitDecisionAnsweredAndCtxCancel(t *testing.T) {
	d, sid := awaitNode(t)
	c := parkRequest(t, d, sid, "r1", bashPermission(""))
	if err := respond(t, d, api.RespondParams{SessionID: sid, RequestID: "r1", Kind: "permission", Behavior: "allow"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	if got := <-c.out; got == "" {
		t.Error("answered: want a decision, got empty")
	}
	if ix := head(d, sid); ix != nil {
		t.Errorf("answered: session still shows %+v", ix)
	}

	d, sid = awaitNode(t)
	c = parkRequest(t, d, sid, "r1", bashPermission(""))
	c.cancel()
	if got := <-c.out; got != "" {
		t.Errorf("ctx cancel: out=%q want empty", got)
	}
	if ix := head(d, sid); ix != nil {
		t.Errorf("ctx cancel: session still shows %+v", ix)
	}
	if pendingHas(d, "r1") {
		t.Error("ctx cancel: request still parked")
	}
}

func TestRespondByRequestIDWhileAnotherArrives(t *testing.T) {
	d, sid := awaitNode(t)
	c1 := parkRequest(t, d, sid, "r1", bashPermission(""))
	parkRequest(t, d, sid, "r2", bashPermission(""))
	if err := respond(t, d, api.RespondParams{SessionID: sid, RequestID: "r1", Kind: "permission", Behavior: "allow"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	if got := <-c1.out; got == "" {
		t.Error("r1: want a decision, got empty")
	}
	if ix := head(d, sid); ix == nil || ix.RequestID != "r2" {
		t.Errorf("head = %+v, want r2", ix)
	}
	if !pendingHas(d, "r2") {
		t.Error("r2 must stay parked")
	}
}

func TestRespondWithoutRequestIDAnswersOldest(t *testing.T) {
	d, sid := awaitNode(t)
	c1 := parkRequest(t, d, sid, "r1", bashPermission(""))
	c2 := parkRequest(t, d, sid, "r2", bashPermission(""))
	if err := respond(t, d, api.RespondParams{SessionID: sid, Kind: "permission", Behavior: "allow"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	if got := <-c1.out; got == "" {
		t.Error("r1: want a decision, got empty")
	}
	select {
	case got := <-c2.out:
		t.Errorf("r2 answered too: %q", got)
	default:
	}
	if !pendingHas(d, "r2") {
		t.Error("r2 must stay parked")
	}
	if ix := head(d, sid); ix == nil || ix.RequestID != "r2" {
		t.Errorf("head = %+v, want r2", ix)
	}
}

func TestRespondUnknownRequestIDFails(t *testing.T) {
	d, sid := awaitNode(t)
	err := respond(t, d, api.RespondParams{SessionID: sid, RequestID: "gone", Kind: "permission", Behavior: "allow"})
	if !errors.Is(err, errRequestGone) || err.Error() != errRequestGone.Error() {
		t.Fatalf("err = %v, want errRequestGone without the request id", err)
	}
}

func TestEndingOneRequestKeepsTheOther(t *testing.T) {
	d, sid := awaitNode(t)
	c1 := parkRequest(t, d, sid, "r1", bashPermission(""))
	parkRequest(t, d, sid, "r2", bashPermission(""))
	c1.cancel()
	<-c1.out
	s, _ := d.reg.Get(sid)
	if s.Interaction == nil || s.Interaction.RequestID != "r2" {
		t.Errorf("head = %+v, want r2", s.Interaction)
	}
	if s.Status != session.StatusAwaitingInput {
		t.Errorf("status = %q, want awaiting_input", s.Status)
	}
}

func TestSubagentAnswerLeavesBaseAlone(t *testing.T) {
	d, sid := awaitNode(t)
	d.applyHook(context.Background(), d.adapterFor(""), hookEvent("Stop", `{}`))
	c := parkRequest(t, d, sid, "r1", bashPermission("a1"))
	if err := respond(t, d, api.RespondParams{SessionID: sid, RequestID: "r1", Kind: "permission", Behavior: "allow"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	<-c.out
	s, _ := d.reg.Get(sid)
	if s.Status == session.StatusWorking || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Errorf("status=%q interaction=%+v, want the idle base", s.Status, s.Interaction)
	}
}

func TestStopDuringPendingKeepsRequest(t *testing.T) {
	d, sid := awaitNode(t)
	parkRequest(t, d, sid, "r1", bashPermission(""))
	d.applyHook(context.Background(), d.adapterFor(""), hookEvent("Stop", `{}`))
	if ix := head(d, sid); ix == nil || ix.RequestID != "r1" {
		t.Errorf("head = %+v, want r1", ix)
	}
}

func TestCancelFromNilBaseSurfacesIdle(t *testing.T) {
	d, sid := awaitNode(t)
	d.reg.ClearInteraction(sid)
	c := parkRequest(t, d, sid, "r1", bashPermission(""))
	if err := respond(t, d, api.RespondParams{SessionID: sid, RequestID: "r1", QuestionAction: "cancel"}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	<-c.out
	s, _ := d.reg.Get(sid)
	if s.Status != session.StatusIdle || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Errorf("status=%q interaction=%+v, want idle with the idle interaction", s.Status, s.Interaction)
	}
}

func hookEvent(event, payload string) claudecode.HookEvent {
	return claudecode.HookEvent{Event: event, TmuxPane: "%9", Payload: json.RawMessage(payload)}
}

func TestPermissionPromptNotificationThenCancelLeavesWorking(t *testing.T) {
	d, sid := awaitNode(t)
	c := parkRequest(t, d, sid, "r1", bashPermission(""))
	d.applyHook(context.Background(), d.adapterFor(""), hookEvent("Notification", `{"notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`))
	if ix := head(d, sid); ix == nil || ix.RequestID != "r1" {
		t.Errorf("head = %+v, want r1", ix)
	}
	c.cancel()
	<-c.out
	s, _ := d.reg.Get(sid)
	if s.Status != session.StatusWorking || s.Interaction != nil {
		t.Errorf("status=%q interaction=%+v, want working and nil", s.Status, s.Interaction)
	}
}

func TestEventsDuringPendingKeepRequest(t *testing.T) {
	cases := map[string]claudecode.HookEvent{
		"PreToolUse":               hookEvent("PreToolUse", `{"tool_name":"Bash"}`),
		"idle_prompt Notification": hookEvent("Notification", `{"notification_type":"idle_prompt"}`),
	}
	for name, ev := range cases {
		t.Run(name, func(t *testing.T) {
			d, sid := awaitNode(t)
			parkRequest(t, d, sid, "r1", bashPermission(""))
			d.applyHook(context.Background(), d.adapterFor(""), ev)
			if ix := head(d, sid); ix == nil || ix.RequestID != "r1" {
				t.Errorf("head = %+v, want r1", ix)
			}
		})
	}
}
