package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/mesh"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type meshPrompter struct{ *fakePrompter }

func (meshPrompter) IsHeadless() bool { return false }

func (meshPrompter) CallerEnv() string                                    { return "" }
func (meshPrompter) IsAgentProcess(procwalk.Proc, map[string]string) bool { return false }

func deliverNode(t *testing.T) (*Node, *fakePrompter) {
	t.Helper()
	d, _ := meshNode(t)
	d.id = "home"
	d.adapterList = adapters.All()
	fp := &fakePrompter{}
	d.adapters["opencode"] = meshPrompter{fp}
	return d, fp
}

func apiSession(d *Node, id string, status session.Status, ix *session.Interaction) session.Session {
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "opencode", AgentSessionID: id, Status: status, Interaction: ix, Input: session.InputAPI,
	})
	return s
}

func msgState(t *testing.T, d *Node, id string) api.MessageState {
	t.Helper()
	m, ok, err := d.mesh.Get(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
	}
	return m.State
}

func TestSendDeliversWhenIdle(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, err := d.send(t.Context(), a, "home:"+b.ID, "do the thing")
	if err != nil {
		t.Fatal(err)
	}
	d.meshPass(t.Context())
	if !fp.called || !strings.HasPrefix(fp.gotText, "[argus message m-") || !strings.Contains(fp.gotText, "do the thing") {
		t.Fatalf("called=%v text=%q", fp.called, fp.gotText)
	}
	if st := msgState(t, d, res.MessageID); st != api.MessageDelivered {
		t.Fatalf("state = %s", st)
	}
}

func TestSendWaitsWhileWorking(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusWorking, nil)
	res, err := d.send(t.Context(), a, b.ID, "later")
	if err != nil {
		t.Fatal(err)
	}
	d.meshPass(t.Context())
	if fp.called || msgState(t, d, res.MessageID) != api.MessageQueued {
		t.Fatalf("delivered while working: called=%v", fp.called)
	}
	apiSession(d, "b", session.StatusIdle, nil)
	d.meshPass(t.Context())
	if !fp.called || msgState(t, d, res.MessageID) != api.MessageDelivered {
		t.Fatal("not delivered after idle")
	}
}

func TestSendWaitsWhilePromptPending(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionPermission})
	res, err := d.send(t.Context(), a, b.ID, "hold")
	if err != nil {
		t.Fatal(err)
	}
	d.meshPass(t.Context())
	if fp.called || msgState(t, d, res.MessageID) != api.MessageQueued {
		t.Fatalf("delivered with a pending prompt: called=%v", fp.called)
	}
}

func TestSendToNonAgentSessionFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	plain, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "shell", AgentSessionID: "x", Status: session.StatusIdle})
	_, err := d.send(t.Context(), a, plain.ID, "hi")
	if err == nil || !strings.Contains(err.Error(), "does not support agent messages") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendToSessionWithoutAgentIDFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	hookSession(d, "%3", "")
	want := "default:%3 is not ready yet: its agent session id is unknown"
	if _, err := d.send(t.Context(), a, "default:%3", "hi"); err == nil || err.Error() != want {
		t.Fatalf("agent send err = %v", err)
	}
	if _, err := ownerSend(t, d, api.OwnerSendParams{SessionID: "default:%3", Text: "hi"}); err == nil || err.Error() != want {
		t.Fatalf("owner send err = %v", err)
	}
	if msgs, err := d.mesh.List(t.Context(), 10); err != nil || len(msgs) != 0 {
		t.Fatalf("messages = %+v err=%v", msgs, err)
	}
}

func TestReplyMarksOriginalAndQueuesBack(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, err := d.send(t.Context(), a, b.ID, "q")
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.reply(t.Context(), b, m1.MessageID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if msgState(t, d, m1.MessageID) != api.MessageReplied {
		t.Fatal("original not replied")
	}
	m, _, _ := d.mesh.Get(t.Context(), r.MessageID)
	if m.Kind != api.MessageReply || m.ReplyTo != m1.MessageID || m.To.Key != "opencode:a" || m.State != api.MessageQueued {
		t.Fatalf("reply = %+v", m)
	}
}

func TestReplyByNonRecipientFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	c := apiSession(d, "c", session.StatusIdle, nil)
	m1, _ := d.send(t.Context(), a, b.ID, "q")
	_, err := d.reply(t.Context(), c, m1.MessageID, "x")
	if err == nil || !strings.Contains(err.Error(), "only the recipient") {
		t.Fatalf("err = %v", err)
	}
}

func TestReplyToGoneSenderFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, _ := d.send(t.Context(), a, b.ID, "q")
	d.meshPass(t.Context())
	d.reg.ReconcileSessions("opencode", []registry.DiscoveredSession{{AgentSessionID: "b"}})
	_, err := d.reply(t.Context(), b, m1.MessageID, "a")
	if err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("err = %v", err)
	}
	if st := msgState(t, d, m1.MessageID); st != api.MessageDelivered {
		t.Fatalf("original state = %s", st)
	}
}

func TestQueuedMessagesFailWhenRecipientEnds(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusWorking, nil)
	res, err := d.send(t.Context(), a, b.ID, "q")
	if err != nil {
		t.Fatal(err)
	}
	d.meshPass(t.Context())
	d.reg.ReconcileSessions("opencode", []registry.DiscoveredSession{{AgentSessionID: "a"}})
	d.meshPass(t.Context())
	if fp.called || msgState(t, d, res.MessageID) != api.MessageFailed {
		t.Fatalf("called=%v state=%s", fp.called, msgState(t, d, res.MessageID))
	}
}

func TestDeliveryWaitsForTheTurnOfTheLastMessage(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, _ := d.send(t.Context(), a, b.ID, "one")
	m2, _ := d.send(t.Context(), a, b.ID, "two")
	d.meshPass(t.Context())
	d.meshPass(t.Context())
	if msgState(t, d, m1.MessageID) != api.MessageDelivered || msgState(t, d, m2.MessageID) != api.MessageQueued {
		t.Fatal("second message went out before the first turn started")
	}
	d.meshNoteEvent(registry.Event{Session: session.Session{ID: b.ID, Status: session.StatusWorking}})
	d.meshPass(t.Context())
	if msgState(t, d, m2.MessageID) != api.MessageDelivered || !strings.Contains(fp.gotText, "two") {
		t.Fatal("second message not delivered after the first turn")
	}
}

func TestMeshQueueSurvivesRestart(t *testing.T) {
	path := t.TempDir() + "/argus.db"
	open := func() *Node {
		sqlDB, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { sqlDB.Close() })
		d := newNode(nil)
		d.id = "home"
		d.adapterList = adapters.All()
		d.SetMeshStore(mesh.NewStore(sqlDB, time.Now))
		return d
	}
	d1 := open()
	d1.adapters["opencode"] = meshPrompter{&fakePrompter{}}
	a := apiSession(d1, "a", session.StatusIdle, nil)
	apiSession(d1, "b", session.StatusWorking, nil)
	res, err := d1.send(context.Background(), a, "opencode:b", "persist")
	if err != nil {
		t.Fatal(err)
	}

	d2 := open()
	fp := &fakePrompter{}
	d2.adapters["opencode"] = meshPrompter{fp}
	apiSession(d2, "b", session.StatusIdle, nil)
	d2.meshPass(context.Background())
	if !fp.called || msgState(t, d2, res.MessageID) != api.MessageDelivered {
		t.Fatalf("not delivered after restart: called=%v", fp.called)
	}
}

type hookPrompter struct {
	meshPrompter
	err error
	do  func()
}

func (h hookPrompter) SendPrompt(ctx context.Context, s session.Session, text string) error {
	if h.do != nil {
		h.do()
	}
	if h.err != nil {
		return h.err
	}
	return h.meshPrompter.SendPrompt(ctx, s, text)
}

func TestDeliveryFailureLeavesQueued(t *testing.T) {
	d, fp := deliverNode(t)
	d.adapters["opencode"] = hookPrompter{meshPrompter{fp}, errors.New("boom"), nil}
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, _ := d.send(t.Context(), a, b.ID, "x")
	d.meshPass(t.Context())
	if st := msgState(t, d, res.MessageID); st != api.MessageQueued {
		t.Fatalf("state = %s", st)
	}
}

func TestReplyDuringDeliveryStaysReplied(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, _ := d.send(t.Context(), a, b.ID, "x")
	d.adapters["opencode"] = hookPrompter{meshPrompter{fp}, nil, func() {
		if _, err := d.reply(t.Context(), b, res.MessageID, "done"); err != nil {
			t.Error(err)
		}
	}}
	d.meshPass(t.Context())
	if st := msgState(t, d, res.MessageID); st != api.MessageReplied {
		t.Fatalf("state = %s", st)
	}
}

func TestHandleAgentSendThroughCaller(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	apiSession(d, "b", session.StatusIdle, nil)
	params, _ := json.Marshal(api.AgentSendParams{Caller: caller, SessionID: "home:opencode:b", Text: "hello"})
	out, err := d.handleAgentSend(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(api.AgentSendResult)
	m, ok, _ := d.mesh.Get(t.Context(), res.MessageID)
	if !ok || m.From.Key != "claude:p1" || m.To.Key != "opencode:b" || m.Body != "hello" {
		t.Fatalf("message = %+v ok=%v", m, ok)
	}
}

func TestHandleAgentSendMeshDisabled(t *testing.T) {
	d := newNode(nil)
	params, _ := json.Marshal(api.AgentSendParams{SessionID: "x", Text: "y"})
	if _, err := d.handleAgentSend(t.Context(), params); !errors.Is(err, errMeshDisabled) {
		t.Fatalf("err = %v", err)
	}
}
