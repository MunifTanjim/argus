package node

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// crossPeers is a parent (opencode:p1) on a and its child (opencode:c1) on b,
// linked on both nodes.
type crossPeers struct {
	url           string
	a, b          *Node
	stops         map[string]func()
	parent, child session.Session
	parentFP      *fakePrompter
	childResp     *noticeResponder
}

var (
	crossParent = api.Actor{Node: "a", Key: "opencode:p1"}
	crossChild  = api.Actor{Node: "b", Key: "opencode:c1"}
)

func newCrossPeers(t *testing.T) *crossPeers {
	t.Helper()
	cp := &crossPeers{url: startPeerGateway(t), parentFP: &fakePrompter{}, stops: map[string]func(){}}
	var stopA, stopB func()
	cp.a, stopA = startPeerNodeWith(t, cp.url, "a", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		d.adapters["opencode"] = orchPrompter{cp.parentFP}
		cp.parent = apiSession(d, "p1", session.StatusIdle, nil)
	})
	cp.childResp = &noticeResponder{orchPrompter: orchPrompter{&fakePrompter{}}}
	cp.b, stopB = startPeerNodeWith(t, cp.url, "b", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		d.adapters["opencode"] = cp.childResp
		cp.child = apiSession(d, "c1", session.StatusIdle, nil)
	})
	cp.stops["a"], cp.stops["b"] = stopA, stopB
	waitPeerOnline(t, cp.a, "b", true)
	waitPeerOnline(t, cp.b, "a", true)
	link := api.Link{Child: crossChild, Parent: crossParent}
	for _, d := range []*Node{cp.a, cp.b} {
		if err := d.orch.PutLink(t.Context(), link); err != nil {
			t.Fatal(err)
		}
	}
	return cp
}

func (cp *crossPeers) node(id string) (self, other *Node) {
	if id == "a" {
		return cp.a, cp.b
	}
	return cp.b, cp.a
}

// takeOffline ends the uplink of node id and waits until the other node sees
// it offline.
func (cp *crossPeers) takeOffline(t *testing.T, id string) {
	t.Helper()
	cp.stops[id]()
	_, other := cp.node(id)
	waitFor(t, func() bool {
		n, ok := other.roster.get(id)
		return !ok || !n.Online
	})
}

// bringBack reconnects the uplink of node id.
func (cp *crossPeers) bringBack(t *testing.T, id string) {
	t.Helper()
	self, other := cp.node(id)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); self.ConnectGateway(ctx, cp.url, "", nil) }()
	t.Cleanup(func() { cancel(); <-done })
	waitPeerOnline(t, self, other.id, true)
	waitPeerOnline(t, other, id, true)
}

// flush sends the outbox of node id until it is empty.
func (cp *crossPeers) flush(t *testing.T, id string) {
	t.Helper()
	self, other := cp.node(id)
	waitFor(t, func() bool {
		self.flushOutbox(t.Context())
		left, err := self.orch.Pending(t.Context(), other.id, 10)
		return err == nil && len(left) == 0
	})
}

// sendToChild sends a task from the parent on a to the child on b and has b
// deliver it.
func (cp *crossPeers) sendToChild(t *testing.T) string {
	t.Helper()
	res, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), session.CompositeID("b", cp.child.ID), "build it", "")
	if err != nil {
		t.Fatal(err)
	}
	cp.flush(t, "a")
	cp.b.orchPass(t.Context())
	if st := msgState(t, cp.b, res.MessageID); st != api.MessageDelivered {
		t.Fatalf("task state on b = %s", st)
	}
	return res.MessageID
}

func promptsWith(fp *fakePrompter, s string) int {
	n := 0
	for _, p := range fp.prompts {
		if strings.Contains(p, s) {
			n++
		}
	}
	return n
}

func TestCrossNodeReplyReachesParent(t *testing.T) {
	cp := newCrossPeers(t)
	ctx := peerCtx(t)
	childSID := session.CompositeID("b", cp.child.ID)
	if err := cp.a.orch.PutLink(ctx, api.Link{Child: crossChild, Parent: crossParent, ChildSessionID: childSID}); err != nil {
		t.Fatal(err)
	}
	taskID := cp.sendToChild(t)

	res, err := cp.b.reply(ctx, cp.b.localCallerRef(cp.child), taskID, "built")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.MessageID, "b:m-") {
		t.Fatalf("reply id = %q", res.MessageID)
	}
	if st := msgState(t, cp.b, taskID); st != api.MessageReplied {
		t.Fatalf("task state on b = %s", st)
	}
	cp.flush(t, "b")
	got, ok, err := cp.a.orch.Get(ctx, res.MessageID)
	if err != nil || !ok {
		t.Fatalf("a has no reply: ok=%v err=%v", ok, err)
	}
	if got.From != crossChild || got.To != crossParent || got.ReplyTo != taskID || got.State != api.MessageQueued {
		t.Fatalf("reply on a = %+v", got)
	}
	cp.a.orchPass(t.Context())
	if promptsWith(cp.parentFP, res.MessageID) != 1 || promptsWith(cp.parentFP, "built") != 1 {
		t.Fatalf("parent prompts = %q", cp.parentFP.prompts)
	}
	if !strings.Contains(cp.parentFP.gotText, `from="`+sessionLabel(cp.child)+`" session="`+childSID+`"`) {
		t.Fatalf("prompt = %q", cp.parentFP.gotText)
	}
	if st := msgState(t, cp.a, res.MessageID); st != api.MessageDelivered {
		t.Fatalf("reply state on a = %s", st)
	}

	// The parent answers its copy of the reply on a, which queues the answer for b.
	back, err := cp.a.reply(ctx, cp.a.localCallerRef(cp.parent), res.MessageID, "thanks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(back.MessageID, "a:m-") {
		t.Fatalf("answer id = %q", back.MessageID)
	}
	cp.flush(t, "a")
	m, ok, err := cp.b.orch.Get(ctx, back.MessageID)
	if err != nil || !ok || m.From != crossParent || m.To != crossChild || m.ReplyTo != res.MessageID {
		t.Fatalf("answer on b = %+v ok=%v err=%v", m, ok, err)
	}
	if st := msgState(t, cp.a, res.MessageID); st != api.MessageReplied {
		t.Fatalf("a's copy of the reply = %s", st)
	}
}

func TestCrossNodeReplyToRemoteChildQueuedWhileOffline(t *testing.T) {
	cp := newCrossPeers(t)
	taskID := cp.sendToChild(t)
	res, err := cp.b.reply(t.Context(), cp.b.localCallerRef(cp.child), taskID, "built")
	if err != nil {
		t.Fatal(err)
	}
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())
	// The child's next turn ends the hold the task put on it.
	apiSession(cp.b, "c1", session.StatusWorking, nil)
	cp.b.orchPass(t.Context())
	apiSession(cp.b, "c1", session.StatusIdle, nil)
	cp.takeOffline(t, "b")

	back, err := cp.a.reply(t.Context(), cp.a.localCallerRef(cp.parent), res.MessageID, "thanks")
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := cp.a.orch.Pending(t.Context(), "b", 10); len(delivers(left)) != 1 {
		t.Fatalf("a outbox = %+v", left)
	}

	cp.bringBack(t, "b")
	cp.flush(t, "a")
	cp.a.flushOutbox(t.Context())
	cp.b.orchPass(t.Context())
	cp.b.orchPass(t.Context())
	if n := promptsWith(cp.childResp.fakePrompter, back.MessageID); n != 1 {
		t.Fatalf("answer delivered %d times; prompts = %q", n, cp.childResp.prompts)
	}
}

func TestCrossNodeReplyQueuedWhileParentOffline(t *testing.T) {
	cp := newCrossPeers(t)
	taskID := cp.sendToChild(t)
	cp.takeOffline(t, "a")

	res, err := cp.b.reply(t.Context(), cp.b.localCallerRef(cp.child), taskID, "built")
	if err != nil {
		t.Fatal(err)
	}
	cp.b.flushOutbox(t.Context())
	if left, _ := cp.b.orch.Pending(t.Context(), "a", 10); len(delivers(left)) != 1 {
		t.Fatalf("b outbox = %+v", left)
	}

	cp.bringBack(t, "a")
	cp.flush(t, "b")
	cp.b.flushOutbox(t.Context())
	cp.a.orchPass(t.Context())
	cp.a.orchPass(t.Context())
	if n := promptsWith(cp.parentFP, res.MessageID); n != 1 {
		t.Fatalf("reply delivered %d times; prompts = %q", n, cp.parentFP.prompts)
	}
}

func TestCrossNodeInteractionAndRespond(t *testing.T) {
	cp := newCrossPeers(t)
	ctx := peerCtx(t)
	ix := approvable("go test ./...")
	ix.RequestID = "r1"
	child := apiSession(cp.b, "c1", session.StatusAwaitingInput, ix)
	childSID := session.CompositeID("b", child.ID)
	if err := cp.a.orch.PutLink(ctx, api.Link{Child: crossChild, Parent: crossParent, ChildSessionID: childSID}); err != nil {
		t.Fatal(err)
	}
	cp.b.orchPass(t.Context())
	n := onlyNotice(t, cp.b)
	if !strings.HasPrefix(n.ID, "b:m-") || n.To != crossParent || n.State != api.MessageQueued {
		t.Fatalf("notice on b = %+v", n)
	}
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())
	if promptsWith(cp.parentFP, n.ID) != 1 || !strings.Contains(cp.parentFP.gotText, "go test ./...") {
		t.Fatalf("parent prompts = %q", cp.parentFP.prompts)
	}
	if want := `child-session="` + childSID + `"`; !strings.Contains(cp.parentFP.gotText, want) {
		t.Fatalf("parent prompt lacks %s: %q", want, cp.parentFP.gotText)
	}
	if st := msgState(t, cp.a, n.ID); st != api.MessageDelivered {
		t.Fatalf("notice on a = %s", st)
	}

	if err := cp.a.respond(ctx, cp.a.localCallerRef(cp.parent), api.OrchRespondParams{MessageID: n.ID, Answers: map[string]any{"Proceed?": "Yes"}}); err != nil {
		t.Fatal(err)
	}
	if len(cp.childResp.got) != 1 || cp.childResp.got[0].SessionID != child.ID || cp.childResp.got[0].RequestID != "r1" {
		t.Fatalf("child responder got %+v", cp.childResp.got)
	}
	got, _, _ := cp.b.orch.Get(ctx, n.ID)
	if got.State != api.MessageReplied || got.AnsweredBy == nil || *got.AnsweredBy != (api.Actor{Node: "a", Key: "opencode:p1"}) {
		t.Fatalf("notice on b = %+v", got)
	}
	if st := msgState(t, cp.a, n.ID); st != api.MessageReplied {
		t.Fatalf("notice on a = %s", st)
	}
}
func TestRespondToDisconnectedNodeNamesTheNode(t *testing.T) {
	cp := newCrossPeers(t)
	ctx := peerCtx(t)
	apiSession(cp.b, "c1", session.StatusAwaitingInput, approvable("go test ./..."))
	cp.b.orchPass(t.Context())
	n := onlyNotice(t, cp.b)
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())
	cp.a.roster.reset()

	err := cp.a.respond(ctx, cp.a.localCallerRef(cp.parent), api.OrchRespondParams{MessageID: n.ID, Answers: map[string]any{"Proceed?": "Yes"}})
	if err == nil || !strings.Contains(err.Error(), "node b, which is not connected") {
		t.Fatalf("err = %v", err)
	}
	if st := msgState(t, cp.a, n.ID); st != api.MessageDelivered {
		t.Fatalf("notice on a = %s", st)
	}
}

func TestReplyToUnknownMessageOnDisconnectedNode(t *testing.T) {
	cp := newCrossPeers(t)
	cp.a.roster.reset()
	_, err := cp.a.reply(peerCtx(t), cp.a.localCallerRef(cp.parent), "b:m-missing", "hi")
	if err == nil || !strings.Contains(err.Error(), "node b, which is not connected") {
		t.Fatalf("err = %v", err)
	}
}

func TestOutboxWithdrawSupersedesQueuedNotice(t *testing.T) {
	cp := newCrossPeers(t)
	cp.takeOffline(t, "a")
	apiSession(cp.b, "c1", session.StatusAwaitingInput, approvable("rm -rf build"))
	cp.b.orchPass(t.Context())
	n := onlyNotice(t, cp.b)
	cp.b.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "c1", Status: session.StatusIdle, Input: session.InputAPI})
	cp.b.orchPass(t.Context())
	if st := msgState(t, cp.b, n.ID); st != api.MessageWithdrawn {
		t.Fatalf("notice on b = %s", st)
	}
	// The withdraw supersedes the deliver still queued.
	if left, _ := cp.b.orch.Pending(t.Context(), "a", 10); len(left) != 1 || left[0].Method != api.MethodOrchPeerWithdraw {
		t.Fatalf("b outbox = %+v", left)
	}

	cp.bringBack(t, "a")
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())
	cp.a.orchPass(t.Context())
	if len(cp.parentFP.prompts) != 0 {
		t.Fatalf("parent prompts = %q", cp.parentFP.prompts)
	}
	if m, ok, _ := cp.a.orch.Get(t.Context(), n.ID); ok && m.State != api.MessageWithdrawn {
		t.Fatalf("notice on a = %+v", m)
	}
	if ms := messagesOf(t, cp.a, api.MessageInteractionEnded); len(ms) != 0 {
		t.Fatalf("answered notices on a = %+v", ms)
	}
}

func TestCrossNodeWithdrawDeliveredNoticeTellsParent(t *testing.T) {
	cp := newCrossPeers(t)
	apiSession(cp.b, "c1", session.StatusAwaitingInput, approvable("rm -rf build"))
	cp.b.orchPass(t.Context())
	n := onlyNotice(t, cp.b)
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())
	if st := msgState(t, cp.a, n.ID); st != api.MessageDelivered {
		t.Fatalf("notice on a = %s", st)
	}

	cp.b.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "c1", Status: session.StatusIdle, Input: session.InputAPI})
	cp.b.orchPass(t.Context())
	cp.flush(t, "b")
	// A repeated withdraw adds nothing.
	if err := cp.b.orch.Enqueue(t.Context(), "a", api.MethodOrchPeerWithdraw, api.OrchPeerWithdrawParams{MessageID: n.ID, Ended: "again"}); err != nil {
		t.Fatal(err)
	}
	cp.flush(t, "b")
	if st := msgState(t, cp.a, n.ID); st != api.MessageWithdrawn {
		t.Fatalf("notice on a = %s", st)
	}
	ms := messagesOf(t, cp.a, api.MessageInteractionEnded)
	if len(ms) != 1 || ms[0].To != crossParent || !strings.Contains(ms[0].Body, "answered outside argus") {
		t.Fatalf("answered notices on a = %+v", ms)
	}
	if ms := messagesOf(t, cp.b, api.MessageInteractionEnded); len(ms) != 0 {
		t.Fatalf("answered notices on b = %+v", ms)
	}
}

func TestCrossNodeTurnEndedReachesParent(t *testing.T) {
	cp := newCrossPeers(t)
	// A task as a spawn from a stores it on b.
	task, err := cp.b.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageTask, From: crossParent, To: crossChild, Body: "build it", State: api.MessageDelivered,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID := task.ID
	apiSession(cp.b, "c1", session.StatusWorking, nil)
	cp.b.orchPass(t.Context())
	apiSession(cp.b, "c1", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	cp.b.orchPass(t.Context())
	cp.b.orchPass(t.Context())
	ns := messagesOf(t, cp.b, api.MessageTurnEnded)
	if len(ns) != 1 || ns[0].To != crossParent || !strings.Contains(ns[0].Body, taskID) {
		t.Fatalf("turn notices on b = %+v", ns)
	}
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())
	if promptsWith(cp.parentFP, ns[0].ID) != 1 {
		t.Fatalf("parent prompts = %q", cp.parentFP.prompts)
	}
}

func TestFailGoneIgnoresRemoteRecipients(t *testing.T) {
	d := pushNode(t)
	from := api.Actor{Node: "a", Key: "claude:p"}
	remote, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageReply, From: from, To: api.Actor{Node: "b", Key: "opencode:k"},
		Body: "thanks", State: api.MessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	local, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: from, To: api.Actor{Node: "a", Key: "opencode:gone"},
		Body: "hi", State: api.MessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(d.orchSettled)
	d.orchPass(t.Context())
	if st := msgState(t, d, remote.ID); st != api.MessageQueued {
		t.Fatalf("remote recipient's message = %s", st)
	}
	if st := msgState(t, d, local.ID); st != api.MessageFailed {
		t.Fatalf("local gone recipient's message = %s", st)
	}
}

func TestSenderLabelRemoteActorNeverMatchesLocal(t *testing.T) {
	d, _ := deliverNode(t)
	apiSession(d, "x", session.StatusIdle, nil)
	if label, sid := d.senderLabel(api.Actor{Node: "b", Key: "opencode:x"}); label != "b:opencode:x" || sid != "" {
		t.Fatalf("label = %q session = %q", label, sid)
	}
}

func TestOwnerReplyNeedsLocalOwnerMessage(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	m, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: d.actor(a), To: api.Actor{Node: "b", Key: api.ActorOwner},
		Body: "hi", State: api.MessageDelivered,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"reply_to":"` + m.ID + `","text":"ok"}`)
	if _, err := d.handleMessageSend(t.Context(), raw); err == nil {
		t.Fatal("owner answered another node's owner message")
	}
}

// orchNodeDB is a node with ID id and an orch store, and the store's database.

func TestNoticeInsertAndEnqueueAtomic(t *testing.T) {
	d, sqlDB := orchNodeDB(t, "b")
	d.adapterList = adapters.All()
	d.adapters["opencode"] = orchPrompter{&fakePrompter{}}
	if err := d.orch.PutLink(t.Context(), api.Link{Child: crossChild, Parent: crossParent}); err != nil {
		t.Fatal(err)
	}
	apiSession(d, "c1", session.StatusAwaitingInput, approvable("make"))
	mustExec(t, sqlDB, `CREATE TRIGGER fail_outbox BEFORE INSERT ON orch_peer_outbox BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	d.orchPass(t.Context())
	if ns := messagesOf(t, d, api.MessageInteraction); len(ns) != 0 {
		t.Fatalf("notice stored without its push: %+v", ns)
	}

	mustExec(t, sqlDB, `DROP TRIGGER fail_outbox`)
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	left, err := d.orch.Pending(t.Context(), "a", 10)
	if err != nil || len(left) != 1 || left[0].Method != api.MethodOrchPeerDeliver || !strings.Contains(string(left[0].Params), n.ID) {
		t.Fatalf("outbox = %+v err=%v", left, err)
	}
}

func TestWithdrawReceiverAtomic(t *testing.T) {
	d, sqlDB := orchNodeDB(t, "a")
	ctx := withPeerNode(t.Context(), "b")
	notice := peerMessage("b:m-00000001", "b")
	notice.Kind, notice.From.Key, notice.State = api.MessageInteraction, api.ActorArgus, api.MessageDelivered
	if _, err := d.orch.Receive(t.Context(), notice); err != nil {
		t.Fatal(err)
	}
	mustExec(t, sqlDB, `CREATE TRIGGER fail_answered BEFORE INSERT ON orch_message
		WHEN NEW.kind = 'interaction_ended' BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	params := api.OrchPeerWithdrawParams{MessageID: notice.ID, Ended: "ended"}
	if err := pushTo(ctx, d, api.MethodOrchPeerWithdraw, params); err == nil {
		t.Fatal("withdraw succeeded without its answered notice")
	}
	if st := msgState(t, d, notice.ID); st != api.MessageDelivered {
		t.Fatalf("copy = %s, want delivered", st)
	}

	mustExec(t, sqlDB, `DROP TRIGGER fail_answered`)
	if err := pushTo(ctx, d, api.MethodOrchPeerWithdraw, params); err != nil {
		t.Fatal(err)
	}
	if st := msgState(t, d, notice.ID); st != api.MessageWithdrawn {
		t.Fatalf("copy = %s, want withdrawn", st)
	}
	if ms := messagesOf(t, d, api.MessageInteractionEnded); len(ms) != 1 || ms[0].Body != "ended" {
		t.Fatalf("answered notices = %+v", ms)
	}
}

// remoteTree is a claude parent p1 on a and its claude child c1 on b, linked on
// both nodes, each removable through a recordingDismisser.
type remoteTree struct {
	*crossPeers
	rdA, rdB *recordingDismisser
}

var (
	treeParent = api.Actor{Node: "a", Key: "claude:p1"}
	treeChild  = api.Actor{Node: "b", Key: "claude:c1"}
)

func newRemoteTree(t *testing.T) *remoteTree {
	t.Helper()
	rt := &remoteTree{
		crossPeers: &crossPeers{url: startPeerGateway(t), stops: map[string]func(){}},
		rdA:        &recordingDismisser{fail: map[string]bool{}},
		rdB:        &recordingDismisser{fail: map[string]bool{}},
	}
	var stopA, stopB func()
	rt.a, stopA = startPeerNodeWith(t, rt.url, "a", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		d.adapters["claude"] = rt.rdA
		d.addAgentSession("claude", "p1")
		rt.parent, _ = d.reg.Get("default:%p1")
	})
	rt.b, stopB = startPeerNodeWith(t, rt.url, "b", mustKeyPair(t), true, func(d *Node) {
		withOrchStore(t, d)
		d.adapters["claude"] = rt.rdB
		d.addAgentSession("claude", "c1")
		rt.child, _ = d.reg.Get("default:%c1")
	})
	rt.stops["a"], rt.stops["b"] = stopA, stopB
	waitPeerOnline(t, rt.a, "b", true)
	waitPeerOnline(t, rt.b, "a", true)
	if err := rt.b.orch.PutLink(t.Context(), api.Link{Child: treeChild, Parent: treeParent, ParentSessionID: "a:claude:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.a.orch.PutLink(t.Context(), api.Link{Child: treeChild, Parent: treeParent, ChildSessionID: "b:claude:c1"}); err != nil {
		t.Fatal(err)
	}
	return rt
}

func linkOf(t *testing.T, d *Node, child api.Actor) api.Link {
	t.Helper()
	l, ok, err := d.orch.Link(t.Context(), child)
	if err != nil || !ok {
		t.Fatalf("%s: link %+v: ok=%v err=%v", d.id, child, ok, err)
	}
	return l
}

func TestKillTreeKillsRemoteChildren(t *testing.T) {
	rt := newRemoteTree(t)
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b:claude:c1", "a:claude:p1"}; !slices.Equal(res.Killed, want) || len(res.NotKilled) != 0 {
		t.Fatalf("result = %+v, want killed %v", res, want)
	}
	if !slices.Equal(rt.rdB.dismissed, []string{"c1"}) {
		t.Fatalf("dismissed on b = %v", rt.rdB.dismissed)
	}
	if by := linkOf(t, rt.b, treeChild).EndedBy; by != "ancestor" {
		t.Fatalf("b link ended by %q", by)
	}
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "ancestor" {
		t.Fatalf("a mirror ended by %q", by)
	}
}

func TestKillTreeReportsOfflineChildren(t *testing.T) {
	rt := newRemoteTree(t)
	rt.takeOffline(t, "b")
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Killed, []string{"a:claude:p1"}) || !slices.Equal(res.NotKilled, []string{"b:claude:c1"}) {
		t.Fatalf("result = %+v", res)
	}
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "" {
		t.Fatalf("a mirror ended by %q", by)
	}
}

func TestKillTreeSkipsEndedRemoteChildren(t *testing.T) {
	rt := newRemoteTree(t)
	if err := rt.a.orch.SetEndedBy(t.Context(), treeChild, "self"); err != nil {
		t.Fatal(err)
	}
	rt.takeOffline(t, "b")
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil || !slices.Equal(res.Killed, []string{"a:claude:p1"}) || len(res.NotKilled) != 0 {
		t.Fatalf("result = %+v err=%v", res, err)
	}
}

func TestAncestorKillNeedsRemoteParent(t *testing.T) {
	rt := newRemoteTree(t)
	rt.b.addAgentSession("claude", "x")
	x, _ := rt.b.reg.Get("default:%x")
	if err := rt.b.orch.PutLink(t.Context(), api.Link{Child: treeChild, Parent: rt.b.actor(x)}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.b.killAs(t.Context(), rt.b.localCallerRef(x), "b:claude:c1", "", true); err != nil {
		t.Fatal(err)
	}
	if by := linkOf(t, rt.b, treeChild).EndedBy; by != "parent" {
		t.Fatalf("a local parent's ancestor kill ended the child by %q", by)
	}
}

func TestRemoteChildEndUpdatesMirror(t *testing.T) {
	rt := newRemoteTree(t)
	if _, err := rt.b.kill(peerCtx(t), rt.b.localCallerRef(rt.child), "b:claude:c1"); err != nil {
		t.Fatal(err)
	}
	rt.flush(t, "b")
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "self" {
		t.Fatalf("a mirror ended by %q", by)
	}
}

func TestParentRefRemoteParent(t *testing.T) {
	rt := newRemoteTree(t)
	want := session.ParentRef{ID: "a:claude:p1"}
	if ref := rt.b.parentRef(rt.child); ref == nil || *ref != want {
		t.Fatalf("parent ref = %+v, want %+v", ref, want)
	}
	if got := rt.b.composite(rt.child).Parent; got == nil || *got != want {
		t.Fatalf("composite parent = %+v, want %+v", got, want)
	}
	if err := rt.b.orch.SetEndedBy(t.Context(), treeChild, "owner"); err != nil {
		t.Fatal(err)
	}
	if ref := rt.b.parentRef(rt.child); ref == nil || !ref.Gone {
		t.Fatalf("parent ref after the link ended = %+v", ref)
	}
}

func TestRemoteChildExitUpdatesMirror(t *testing.T) {
	rt := newRemoteTree(t)
	ctx := t.Context()
	rt.b.orchGoneGrace = 0
	rt.b.orchPass(ctx)
	rt.b.reg.ReconcileSessions("claude", nil)
	rt.b.orchPass(ctx)
	if by := linkOf(t, rt.b, treeChild).EndedBy; by != "self" {
		t.Fatalf("b link ended by %q", by)
	}
	rt.flush(t, "b")
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "self" {
		t.Fatalf("a mirror ended by %q", by)
	}
	rt.b.orchPass(ctx)
	if left, err := rt.b.orch.Pending(ctx, "a", 10); err != nil || len(left) != 0 {
		t.Fatalf("b queued again: %+v err=%v", left, err)
	}
}

func TestKillRemoteChildAlreadyGone(t *testing.T) {
	rt := newRemoteTree(t)
	rt.b.markOrchSettled()
	rt.b.reg.ReconcileSessions("claude", nil)
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Killed, []string{"a:claude:p1"}) || len(res.NotKilled) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "self" {
		t.Fatalf("a mirror ended by %q", by)
	}
}

func TestOwnerKillWithUnreachableRemoteChild(t *testing.T) {
	rt := newRemoteTree(t)
	rt.takeOffline(t, "b")
	out, err := rt.a.handleSessionKill(peerCtx(t), mustJSON(api.SessionRef{SessionID: rt.parent.ID}))
	if err != nil {
		t.Fatal(err)
	}
	if res, ok := out.(api.SessionKillResult); !ok || !slices.Equal(res.NotKilled, []string{"b:claude:c1"}) {
		t.Fatalf("result = %#v", out)
	}
	if !slices.Equal(rt.rdA.dismissed, []string{"p1"}) {
		t.Fatalf("dismissed on a = %v", rt.rdA.dismissed)
	}
}

// TestNestedCrossNodeKill kills p1 on a, whose child c1 is on b, whose child g1
// is back on a.
func TestNestedCrossNodeKill(t *testing.T) {
	rt := newRemoteTree(t)
	rt.a.addAgentSession("claude", "g1")
	grand := api.Actor{Node: "a", Key: "claude:g1"}
	if err := rt.a.orch.PutLink(t.Context(), api.Link{Child: grand, Parent: treeChild, ParentSessionID: "b:claude:c1"}); err != nil {
		t.Fatal(err)
	}
	if err := rt.b.orch.PutLink(t.Context(), api.Link{Child: grand, Parent: treeChild, ChildSessionID: "a:claude:g1"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("kill took %s", d)
	}
	if want := []string{"a:claude:g1", "b:claude:c1", "a:claude:p1"}; !slices.Equal(res.Killed, want) || len(res.NotKilled) != 0 {
		t.Fatalf("result = %+v, want killed %v", res, want)
	}
	for _, c := range []struct {
		d     *Node
		child api.Actor
	}{{rt.a, grand}, {rt.b, grand}, {rt.a, treeChild}, {rt.b, treeChild}} {
		if by := linkOf(t, c.d, c.child).EndedBy; by != "ancestor" {
			t.Errorf("%s: %+v ended by %q", c.d.id, c.child, by)
		}
	}
}

func TestOrchSeedGoneRetriesAfterLinkReadError(t *testing.T) {
	d, sqlDB := orchNodeDB(t, "a")
	child := api.Actor{Node: "a", Key: "opencode:c"}
	if err := d.orch.PutLink(t.Context(), api.Link{Child: child, Parent: api.Actor{Node: "b", Key: "claude:p"}}); err != nil {
		t.Fatal(err)
	}
	close(d.orchSettled)
	now := time.Now()

	mustExec(t, sqlDB, `ALTER TABLE orch_link RENAME TO orch_link_hidden`)
	d.orchSeedGone(t.Context(), nil, now)
	if _, gone := d.orchGone[child.Key]; gone || d.orchSeeded {
		t.Fatalf("seeded after a failed link read: gone=%v seeded=%v", gone, d.orchSeeded)
	}

	mustExec(t, sqlDB, `ALTER TABLE orch_link_hidden RENAME TO orch_link`)
	d.orchSeedGone(t.Context(), nil, now)
	if _, gone := d.orchGone[child.Key]; !gone {
		t.Fatal("the child of a remote parent was not seeded on the retry")
	}
}

func TestDeliveryRendersRecordedRemoteSender(t *testing.T) {
	d, _ := deliverNode(t)
	from := api.Actor{Node: "b", Key: "opencode:x"}
	to := api.Actor{Node: d.id, Key: "claude:a"}
	dl := d.delivery(t.Context(), api.Message{ID: "b:m-00000001", Kind: api.MessageText, From: from, To: to,
		FromSessionID: "b:opencode:x", FromLabel: "planner"})
	if dl.From != "planner" || dl.FromSession != "b:opencode:x" {
		t.Fatalf("from = %q session = %q", dl.From, dl.FromSession)
	}
	// A message from before the sender was recorded keeps the old rendering.
	dl = d.delivery(t.Context(), api.Message{ID: "b:m-00000002", Kind: api.MessageText, From: from, To: to})
	if dl.From != "b:opencode:x" || dl.FromSession != "" {
		t.Fatalf("old message: from = %q session = %q", dl.From, dl.FromSession)
	}
	// argus on another node renders as argus.
	dl = d.delivery(t.Context(), api.Message{ID: "b:m-00000003", Kind: api.MessageTurnEnded,
		From: api.Actor{Node: "b", Key: api.ActorArgus}, To: to})
	if dl.From != api.ActorArgus || dl.FromSession != "" {
		t.Fatalf("argus: from = %q session = %q", dl.From, dl.FromSession)
	}
}

func TestRoutedRespondClosesParentCopyWithoutReply(t *testing.T) {
	cp := newCrossPeers(t)
	ix := approvable("go test ./...")
	ix.RequestID = "r1"
	apiSession(cp.b, "c1", session.StatusAwaitingInput, ix)
	cp.b.orchPass(t.Context())
	n := onlyNotice(t, cp.b)
	cp.flush(t, "b")
	cp.a.orchPass(t.Context())

	// The respond reached b, but its reply never reached a.
	params, _ := json.Marshal(api.OrchRespondParams{
		Caller: cp.a.localCallerRef(cp.parent).remote(), MessageID: n.ID, Answers: map[string]any{"Proceed?": "Yes"},
	})
	if _, err := cp.b.handleOrchInteractionRespond(withPeerNode(peerCtx(t), "a"), params); err != nil {
		t.Fatal(err)
	}
	if st := msgState(t, cp.a, n.ID); st != api.MessageDelivered {
		t.Fatalf("notice on a before the flush = %s", st)
	}
	cp.flush(t, "b")
	got, _, _ := cp.a.orch.Get(t.Context(), n.ID)
	if got.State != api.MessageReplied || got.AnsweredBy == nil || *got.AnsweredBy != (api.Actor{Node: "a", Key: "opencode:p1"}) {
		t.Fatalf("notice on a = %+v", got)
	}
	if ns := messagesOf(t, cp.a, api.MessageInteractionEnded); len(ns) != 0 {
		t.Fatalf("parent got an answered notice for its own answer: %+v", ns)
	}
}

func TestRemoteParentExitReachesChildNode(t *testing.T) {
	rt := newRemoteTree(t)
	ctx := t.Context()
	task := api.Message{
		ID: "a:m-00000001", Kind: api.MessageText, From: treeParent, To: treeChild, Body: "build it",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := pushTo(withPeerNode(ctx, "a"), rt.b, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: task}); err != nil {
		t.Fatal(err)
	}
	rt.a.orchGoneGrace = 0
	rt.a.orchPass(ctx)
	rt.a.reg.ReconcileSessions("claude", nil)
	rt.a.orchPass(ctx)
	if !linkOf(t, rt.a, treeChild).ParentEnded {
		t.Fatal("a mirror does not record that the parent ended")
	}
	rt.flush(t, "a")
	if ref := rt.b.parentRef(rt.child); ref == nil || !ref.Gone {
		t.Fatalf("parent ref on b = %+v", ref)
	}
	_, err := rt.b.reply(ctx, rt.b.localCallerRef(rt.child), task.ID, "built")
	if err == nil || !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("reply to the ended parent: err = %v", err)
	}
	rt.a.orchPass(ctx)
	if left, err := rt.a.orch.Pending(ctx, "b", 10); err != nil || len(left) != 0 {
		t.Fatalf("a queued again: %+v err=%v", left, err)
	}
}

func TestDeliverToGoneKeyFails(t *testing.T) {
	rt := newRemoteTree(t)
	ctx := t.Context()
	rt.b.orchGoneGrace = 0
	rt.b.markOrchSettled()
	rt.b.orchPass(ctx)
	m := api.Message{
		ID: "a:m-00000002", Kind: api.MessageText, From: treeParent, To: api.Actor{Node: "b", Key: "claude:gone"},
		Body: "hi", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := pushTo(withPeerNode(ctx, "a"), rt.b, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m}); err != nil {
		t.Fatal(err)
	}
	rt.b.orchPass(ctx)
	rt.b.orchPass(ctx)
	if st := msgState(t, rt.b, m.ID); st != api.MessageFailed {
		t.Fatalf("message to a gone key = %s", st)
	}
}

func TestRemoteKillFindsResumedChild(t *testing.T) {
	rt := newRemoteTree(t)
	rt.b.markOrchSettled()
	// The mirror holds the session id from spawn; the child now runs elsewhere.
	if err := rt.a.orch.PutLink(t.Context(), api.Link{Child: treeChild, Parent: treeParent, ChildSessionID: "b:claude:old"}); err != nil {
		t.Fatal(err)
	}
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Killed, "b:claude:c1") || len(res.NotKilled) != 0 {
		t.Fatalf("result = %+v, want the child killed by its key", res)
	}
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "ancestor" {
		t.Fatalf("a mirror ended by %q", by)
	}
}

func TestRemoteKillBeforeSettleIsNotKilled(t *testing.T) {
	rt := newRemoteTree(t)
	gone := api.Actor{Node: "b", Key: "claude:gone"}
	if err := rt.a.orch.PutLink(t.Context(), api.Link{Child: gone, Parent: treeParent, ChildSessionID: "b:claude:gone"}); err != nil {
		t.Fatal(err)
	}
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.NotKilled, "b:claude:gone") {
		t.Fatalf("result = %+v, want the unknown child not killed", res)
	}
	if by := linkOf(t, rt.a, gone).EndedBy; by != "" {
		t.Fatalf("a mirror ended by %q before b settled", by)
	}
}

func TestCrossNodeSendIsOwnedBySender(t *testing.T) {
	cp := newCrossPeers(t)
	res, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), session.CompositeID("b", cp.child.ID), "hi", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.MessageID, "a:") {
		t.Fatalf("message id = %s, want one created on a", res.MessageID)
	}
	m, ok, _ := cp.a.orch.Get(t.Context(), res.MessageID)
	if !ok || m.To != crossChild || m.State != api.MessageQueued {
		t.Fatalf("a's message = %+v ok=%v", m, ok)
	}
	cp.flush(t, "a")
	if st := msgState(t, cp.b, res.MessageID); st != api.MessageQueued {
		t.Fatalf("b's copy state = %s", st)
	}
}

func TestCrossNodeSendStateComesBack(t *testing.T) {
	cp := newCrossPeers(t)
	id := cp.sendToChild(t)
	cp.flush(t, "b")
	if st := msgState(t, cp.a, id); st != api.MessageDelivered {
		t.Fatalf("a's copy after delivery = %s", st)
	}
	if _, err := cp.b.reply(peerCtx(t), cp.b.localCallerRef(cp.child), id, "done"); err != nil {
		t.Fatal(err)
	}
	cp.flush(t, "b")
	if st := msgState(t, cp.a, id); st != api.MessageReplied {
		t.Fatalf("a's copy after the reply = %s", st)
	}
}

func TestCrossNodeSendFailsFastForUnknownSession(t *testing.T) {
	cp := newCrossPeers(t)
	cp.b.markOrchSettled()
	if _, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), "b:claude:nope", "hi", ""); err == nil {
		t.Fatal("send to an unknown session on b succeeded")
	}
	if ms := messagesOf(t, cp.a, api.MessageText); len(ms) != 0 {
		t.Fatalf("a stored %+v", ms)
	}
}

func TestCrossNodeSendToLinkedChildWhileOffline(t *testing.T) {
	cp := newCrossPeers(t)
	childID := session.CompositeID("b", cp.child.ID)
	if err := cp.a.orch.PutLink(t.Context(), api.Link{Child: crossChild, Parent: crossParent, ChildSessionID: childID}); err != nil {
		t.Fatal(err)
	}
	cp.takeOffline(t, "b")
	res, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), childID, "later", "")
	if err != nil {
		t.Fatalf("send to an offline linked child: %v", err)
	}
	left, _ := cp.a.orch.Pending(t.Context(), "b", 10)
	if len(left) != 1 || left[0].Method != api.MethodOrchPeerDeliver || !strings.Contains(string(left[0].Params), res.MessageID) {
		t.Fatalf("a's outbox = %+v", left)
	}
}

// delivers keeps the deliver pushes of an outbox, without state reports.
func delivers(items []orch.OutboxItem) []orch.OutboxItem {
	var out []orch.OutboxItem
	for _, it := range items {
		if it.Method == api.MethodOrchPeerDeliver {
			out = append(out, it)
		}
	}
	return out
}

func TestCrossNodeSendThroughLinkStillFailsFast(t *testing.T) {
	cp := newCrossPeers(t)
	cp.b.markOrchSettled()
	gone := "b:claude:gone"
	if err := cp.a.orch.PutLink(t.Context(), api.Link{Child: api.Actor{Node: "b", Key: "opencode:gone"}, Parent: crossParent, ChildSessionID: gone}); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), gone, "hi", ""); err == nil {
		t.Fatal("send to an ended child on an online node succeeded")
	}
	cp.takeOffline(t, "b")
	ended := api.Link{Child: api.Actor{Node: "b", Key: "opencode:gone"}, Parent: crossParent, ChildSessionID: gone, EndedBy: "self"}
	if err := cp.a.orch.PutLink(t.Context(), ended); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), gone, "hi", ""); err == nil {
		t.Fatal("send to an ended child on an offline node succeeded")
	}
}

func TestCrossNodeSendRetryWhileOfflineReturnsFirst(t *testing.T) {
	cp := newCrossPeers(t)
	target := session.CompositeID("b", cp.child.ID)
	first, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), target, "build it", "r1")
	if err != nil {
		t.Fatal(err)
	}
	cp.takeOffline(t, "b")
	again, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), target, "build it", "r1")
	if err != nil || again.MessageID != first.MessageID {
		t.Fatalf("retry = %+v, %v; want %s", again, err, first.MessageID)
	}
	if _, err := cp.a.send(peerCtx(t), cp.a.localCallerRef(cp.parent), target, "something else", "r1"); err == nil {
		t.Fatal("a reused request_id with other text succeeded")
	}
}

func TestClearedChildEndsItsLinkOnTheParentNode(t *testing.T) {
	rt := newRemoteTree(t)
	ctx := t.Context()
	rt.b.orchGoneGrace = 0
	rt.b.orchPass(ctx)
	clearSession(rt.b, "%c1", "c2")
	rt.b.orchPass(ctx)
	if by := linkOf(t, rt.b, treeChild).EndedBy; by != "self" {
		t.Fatalf("b link ended by %q", by)
	}
	rt.flush(t, "b")
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "self" {
		t.Fatalf("a mirror ended by %q", by)
	}
	if _, ok, _ := rt.a.orch.Link(ctx, api.Actor{Node: "b", Key: "claude:c2"}); ok {
		t.Fatal("a mirror moved to the new conversation")
	}
}

func TestClearedParentEndsOnTheChildNode(t *testing.T) {
	rt := newRemoteTree(t)
	ctx := t.Context()
	rt.a.orchGoneGrace = 0
	rt.a.orchPass(ctx)
	clearSession(rt.a, "%p1", "p2")
	rt.a.orchPass(ctx)
	rt.flush(t, "a")
	if l := linkOf(t, rt.b, treeChild); l.Parent != treeParent || !l.ParentEnded {
		t.Fatalf("b link = %+v, want the old parent, ended", l)
	}
}

func TestDeliverToClearedKeyKeepsItsKey(t *testing.T) {
	rt := newRemoteTree(t)
	rt.b.orchGoneGrace = 0
	clearSession(rt.b, "%c1", "c2")
	rt.b.orchPass(t.Context())
	m := api.Message{
		ID: "a:m-00000001", Kind: api.MessageText, From: treeParent, To: treeChild, Body: "hi",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := pushTo(withPeerNode(t.Context(), "a"), rt.b, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := rt.b.orch.Get(t.Context(), m.ID)
	if err != nil || !ok || got.To.Key != treeChild.Key {
		t.Fatalf("message on b = %+v ok=%v err=%v", got, ok, err)
	}
	rt.b.markOrchSettled()
	rt.b.orchPass(t.Context())
	if got, _, _ := rt.b.orch.Get(t.Context(), m.ID); got.State != api.MessageFailed {
		t.Fatalf("message state = %s, want failed", got.State)
	}
}

func TestRemoteKillOfAClearedChildLeavesTheNewConversation(t *testing.T) {
	rt := newRemoteTree(t)
	rt.b.markOrchSettled()
	// The child ran /clear on b; a's mirror still has the old key.
	clearSession(rt.b, "%c1", "c1-next")
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.Killed, "b:claude:c1") || len(res.NotKilled) > 0 {
		t.Fatalf("result = %+v, want the new conversation left alone and nothing failed", res)
	}
	if _, ok := rt.b.reg.FindByAgentSession("claude", "c1-next"); !ok {
		t.Fatal("the new conversation was removed")
	}
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "self" {
		t.Fatalf("a mirror ended by %q, want self", by)
	}
}

func TestRemoteKillOfAClearedChildBeforeSettleIsNotKilled(t *testing.T) {
	rt := newRemoteTree(t)
	clearSession(rt.b, "%c1", "c1-next")
	res, err := rt.a.kill(peerCtx(t), rt.a.localCallerRef(rt.parent), "a:claude:p1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.NotKilled, "b:claude:c1") {
		t.Fatalf("result = %+v, want the child not killed before b settled", res)
	}
	if by := linkOf(t, rt.a, treeChild).EndedBy; by != "" {
		t.Fatalf("a mirror ended by %q before b settled", by)
	}
	if _, ok := rt.b.reg.FindByAgentSession("claude", "c1-next"); !ok {
		t.Fatal("the new conversation was removed")
	}
}
