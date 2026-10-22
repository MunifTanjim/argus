package node

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type orchPrompter struct{ *fakePrompter }

func (orchPrompter) IsHeadless() bool { return false }

func (orchPrompter) CallerEnv() string                                    { return "" }
func (orchPrompter) IsAgentProcess(procwalk.Proc, map[string]string) bool { return false }

func deliverNode(t *testing.T) (*Node, *fakePrompter) {
	t.Helper()
	d, _ := orchNode(t)
	d.SetIdentity("home", "")
	d.adapterList = adapters.All()
	fp := &fakePrompter{}
	d.adapters["opencode"] = orchPrompter{fp}
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
	m, ok, err := d.orch.Get(t.Context(), id)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
	}
	return m.State
}

func TestSendDeliversWhenIdle(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, err := d.send(t.Context(), d.localCallerRef(a), "home:"+b.ID, "do the thing", "")
	if err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	if !fp.called || !regexp.MustCompile(`^<argus-message id="[^":]+:m-`).MatchString(fp.gotText) || !strings.Contains(fp.gotText, "do the thing") {
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
	res, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "later", "")
	if err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	if fp.called || msgState(t, d, res.MessageID) != api.MessageQueued {
		t.Fatalf("delivered while working: called=%v", fp.called)
	}
	apiSession(d, "b", session.StatusIdle, nil)
	d.orchPass(t.Context())
	if !fp.called || msgState(t, d, res.MessageID) != api.MessageDelivered {
		t.Fatal("not delivered after idle")
	}
}

func TestSendWaitsWhilePromptPending(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionPermission})
	res, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "hold", "")
	if err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	if fp.called || msgState(t, d, res.MessageID) != api.MessageQueued {
		t.Fatalf("delivered with a pending prompt: called=%v", fp.called)
	}
}

func TestSendToNonAgentSessionFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	plain, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "shell", AgentSessionID: "x", Status: session.StatusIdle})
	_, err := d.send(t.Context(), d.localCallerRef(a), plain.ID, "hi", "")
	if err == nil || !strings.Contains(err.Error(), "does not support agent messages") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendToSessionWithoutAgentIDFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	hookSession(d, "%3", "")
	want := "default:%3 is not ready yet: its agent session id is unknown"
	if _, err := d.send(t.Context(), d.localCallerRef(a), "default:%3", "hi", ""); err == nil || err.Error() != want {
		t.Fatalf("agent send err = %v", err)
	}
	if _, err := ownerSend(t, d, api.OwnerSendParams{SessionID: "default:%3", Text: "hi"}); err == nil || err.Error() != want {
		t.Fatalf("owner send err = %v", err)
	}
	if msgs, err := d.orch.List(t.Context(), 10); err != nil || len(msgs) != 0 {
		t.Fatalf("messages = %+v err=%v", msgs, err)
	}
}

func TestReplyMarksOriginalAndQueuesBack(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.reply(t.Context(), d.localCallerRef(b), m1.MessageID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if msgState(t, d, m1.MessageID) != api.MessageReplied {
		t.Fatal("original not replied")
	}
	m, _, _ := d.orch.Get(t.Context(), r.MessageID)
	if m.Kind != api.MessageReply || m.ReplyTo != m1.MessageID || m.To.Key != "opencode:a" || m.State != api.MessageQueued {
		t.Fatalf("reply = %+v", m)
	}
}

func TestReplyByNonRecipientFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	c := apiSession(d, "c", session.StatusIdle, nil)
	m1, _ := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "")
	_, err := d.reply(t.Context(), d.localCallerRef(c), m1.MessageID, "x")
	if err == nil || !strings.Contains(err.Error(), "only the recipient") {
		t.Fatalf("err = %v", err)
	}
}

func TestReplyToGoneSenderFails(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, _ := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "")
	d.orchPass(t.Context())
	d.reg.ReconcileSessions("opencode", []registry.DiscoveredSession{{AgentSessionID: "b"}})
	_, err := d.reply(t.Context(), d.localCallerRef(b), m1.MessageID, "a")
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
	res, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "")
	if err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	d.reg.ReconcileSessions("opencode", []registry.DiscoveredSession{{AgentSessionID: "a"}})
	d.orchPass(t.Context())
	if fp.called || msgState(t, d, res.MessageID) != api.MessageFailed {
		t.Fatalf("called=%v state=%s", fp.called, msgState(t, d, res.MessageID))
	}
}

func TestDeliveryWaitsForTheTurnOfTheLastMessage(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	m1, _ := d.send(t.Context(), d.localCallerRef(a), b.ID, "one", "")
	d.orchPass(t.Context())
	m2, _ := d.send(t.Context(), d.localCallerRef(a), b.ID, "two", "")
	d.orchPass(t.Context())
	if msgState(t, d, m1.MessageID) != api.MessageDelivered || msgState(t, d, m2.MessageID) != api.MessageQueued {
		t.Fatal("second message went out before the first turn started")
	}
	d.orchNoteEvent(registry.Event{Session: session.Session{ID: b.ID, Status: session.StatusWorking}})
	d.orchPass(t.Context())
	if msgState(t, d, m2.MessageID) != api.MessageDelivered || !strings.Contains(fp.gotText, "two") {
		t.Fatal("second message not delivered after the first turn")
	}
}

func TestOrchQueueSurvivesRestart(t *testing.T) {
	path := t.TempDir() + "/argus.db"
	open := func() *Node {
		sqlDB, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { sqlDB.Close() })
		d := newNode(nil)
		d.SetIdentity("home", "")
		d.adapterList = adapters.All()
		d.SetOrchStore(orch.NewStore(sqlDB, time.Now))
		return d
	}
	d1 := open()
	d1.adapters["opencode"] = orchPrompter{&fakePrompter{}}
	a := apiSession(d1, "a", session.StatusIdle, nil)
	apiSession(d1, "b", session.StatusWorking, nil)
	res, err := d1.send(context.Background(), d1.localCallerRef(a), "opencode:b", "persist", "")
	if err != nil {
		t.Fatal(err)
	}

	d2 := open()
	fp := &fakePrompter{}
	d2.adapters["opencode"] = orchPrompter{fp}
	apiSession(d2, "b", session.StatusIdle, nil)
	d2.orchPass(context.Background())
	if !fp.called || msgState(t, d2, res.MessageID) != api.MessageDelivered {
		t.Fatalf("not delivered after restart: called=%v", fp.called)
	}
}

type hookPrompter struct {
	orchPrompter
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
	return h.orchPrompter.SendPrompt(ctx, s, text)
}

func TestDeliveryFailureLeavesQueued(t *testing.T) {
	d, fp := deliverNode(t)
	d.adapters["opencode"] = hookPrompter{orchPrompter{fp}, errors.New("boom"), nil}
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, _ := d.send(t.Context(), d.localCallerRef(a), b.ID, "x", "")
	d.orchPass(t.Context())
	if st := msgState(t, d, res.MessageID); st != api.MessageQueued {
		t.Fatalf("state = %s", st)
	}
}

func TestDeliveryFailureWaitsBeforeRetry(t *testing.T) {
	d, fp := deliverNode(t)
	tries := 0
	d.adapters["opencode"] = hookPrompter{orchPrompter{fp}, errors.New("boom"), func() { tries++ }}
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	if _, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "x", ""); err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	d.orchPass(t.Context())
	if tries != 1 {
		t.Fatalf("tries = %d, want 1 inside the hold", tries)
	}
	d.orchHeld["opencode:b"] = time.Now()
	d.orchPass(t.Context())
	if tries != 2 {
		t.Fatalf("tries = %d, want a retry after the hold", tries)
	}
}

func TestReplyDuringDeliveryStaysReplied(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	res, _ := d.send(t.Context(), d.localCallerRef(a), b.ID, "x", "")
	d.adapters["opencode"] = hookPrompter{orchPrompter{fp}, nil, func() {
		if _, err := d.reply(t.Context(), d.localCallerRef(b), res.MessageID, "done"); err != nil {
			t.Error(err)
		}
	}}
	d.orchPass(t.Context())
	if st := msgState(t, d, res.MessageID); st != api.MessageReplied {
		t.Fatalf("state = %s", st)
	}
}

func TestHandleAgentSendThroughCaller(t *testing.T) {
	d, _, caller := spawnNode(t, true)
	apiSession(d, "b", session.StatusIdle, nil)
	params, _ := json.Marshal(api.OrchSendParams{Caller: caller, SessionID: "home:opencode:b", Text: "hello"})
	out, err := d.handleOrchMessageSend(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(api.OrchSendResult)
	m, ok, _ := d.orch.Get(t.Context(), res.MessageID)
	if !ok || m.From.Key != "claude:p1" || m.To.Key != "opencode:b" || m.Body != "hello" {
		t.Fatalf("message = %+v ok=%v", m, ok)
	}
}

func TestHandleAgentSendOrchDisabled(t *testing.T) {
	d := newNode(nil)
	params, _ := json.Marshal(api.OrchSendParams{SessionID: "x", Text: "y"})
	if _, err := d.handleOrchMessageSend(t.Context(), params); !errors.Is(err, errOrchDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestQueuedMessagesFailForRecipientGoneWhileNodeDown(t *testing.T) {
	d, fp := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	if _, err := d.orch.Insert(t.Context(), api.Message{
		ID: "m-00000001", Kind: api.MessageText, From: d.actor(a),
		To: api.Actor{Node: "home", Key: "opencode:b"}, Body: "q", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	if st := msgState(t, d, "m-00000001"); st != api.MessageQueued {
		t.Fatalf("failed before discovery settled: %s", st)
	}
	close(d.orchSettled)
	d.orchPass(t.Context())
	if fp.called || msgState(t, d, "m-00000001") != api.MessageFailed {
		t.Fatalf("called=%v state=%s", fp.called, msgState(t, d, "m-00000001"))
	}
}

func TestQueuedMessagesStayForRecipientLiveAfterRestart(t *testing.T) {
	d, _ := deliverNode(t)
	a := apiSession(d, "a", session.StatusIdle, nil)
	b := apiSession(d, "b", session.StatusWorking, nil)
	res, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "q", "")
	if err != nil {
		t.Fatal(err)
	}
	close(d.orchSettled)
	d.orchPass(t.Context())
	if st := msgState(t, d, res.MessageID); st != api.MessageQueued {
		t.Fatalf("state = %s, want queued", st)
	}
}

func TestNextBatch(t *testing.T) {
	msg := func(id, from string) api.Message {
		return api.Message{ID: id, From: api.Actor{Node: "home", Key: from}}
	}
	w1, w2 := msg("w1", "opencode:a"), msg("w2", "opencode:c")
	o1, o2 := msg("o1", api.ActorOwner), msg("o2", api.ActorOwner)
	f1 := api.Message{ID: "f1", From: api.Actor{Node: "c", Key: api.ActorOwner}}
	for _, tc := range []struct {
		in   []api.Message
		want []string
	}{
		{[]api.Message{w1, w2}, []string{"w1", "w2"}},
		{[]api.Message{o1, w1}, []string{"o1"}},
		{[]api.Message{w1, o1, w2}, []string{"w1", "w2"}},
		{[]api.Message{o1, o2}, []string{"o1"}},
		{[]api.Message{f1, w1}, []string{"f1", "w1"}},
	} {
		var got []string
		for _, m := range nextBatch("home", tc.in) {
			got = append(got, m.ID)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("nextBatch(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestDeliverGroupsWaitingMessages(t *testing.T) {
	d, fp := deliverNode(t)
	b := apiSession(d, "b", session.StatusWorking, nil)
	var ids []string
	for _, from := range []string{"a", "c", "e"} {
		res, err := d.send(t.Context(), d.localCallerRef(apiSession(d, from, session.StatusIdle, nil)), b.ID, "result from "+from, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.MessageID)
	}
	apiSession(d, "b", session.StatusIdle, nil)
	d.orchPass(t.Context())
	if len(fp.prompts) != 1 || strings.Count(fp.prompts[0], "<argus-message ") != 3 {
		t.Fatalf("prompts = %q", fp.prompts)
	}
	for _, id := range ids {
		if st := msgState(t, d, id); st != api.MessageDelivered {
			t.Errorf("%s state = %s", id, st)
		}
	}
}

func TestDeliverGroupsAroundAnOwnerMessage(t *testing.T) {
	d, fp := deliverNode(t)
	b := apiSession(d, "b", session.StatusWorking, nil)
	a, err := d.send(t.Context(), d.localCallerRef(apiSession(d, "a", session.StatusIdle, nil)), b.ID, "from a", "")
	if err != nil {
		t.Fatal(err)
	}
	o, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: api.Actor{Node: d.id, Key: api.ActorOwner}, To: d.actor(b),
		Body: "from owner", State: api.MessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.send(t.Context(), d.localCallerRef(apiSession(d, "c", session.StatusIdle, nil)), b.ID, "from c", "")
	if err != nil {
		t.Fatal(err)
	}
	apiSession(d, "b", session.StatusIdle, nil)
	d.orchPass(t.Context())
	if len(fp.prompts) != 1 || !strings.Contains(fp.prompts[0], "from a") || !strings.Contains(fp.prompts[0], "from c") ||
		strings.Contains(fp.prompts[0], "from owner") || msgState(t, d, o.ID) != api.MessageQueued {
		t.Fatalf("first prompt = %q", fp.prompts)
	}
	if msgState(t, d, a.MessageID) != api.MessageDelivered || msgState(t, d, c.MessageID) != api.MessageDelivered {
		t.Fatal("worker messages not delivered")
	}
	apiSession(d, "b", session.StatusWorking, nil)
	d.orchPass(t.Context())
	apiSession(d, "b", session.StatusIdle, nil)
	d.orchPass(t.Context())
	if len(fp.prompts) != 2 || strings.Count(fp.prompts[1], "<argus-message ") != 1 || !strings.Contains(fp.prompts[1], "from owner") {
		t.Fatalf("second prompt = %q", fp.prompts)
	}
}

// paneTest is a node with one pane-input recipient whose deliveries and pane
// probe are faked.
type paneTest struct {
	d         *Node
	s         session.Session
	delivered []string
	activity  time.Time
	screen    string
	probes    int
}

func newPaneTest(t *testing.T) *paneTest {
	t.Helper()
	d, _ := deliverNode(t)
	pt := &paneTest{d: d}
	pt.s, _ = d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%t1",
		AgentSessionID: "t1", Status: session.StatusIdle, Input: session.InputPane,
	})
	d.deliverFn = func(_ context.Context, s session.Session, text string) error {
		pt.delivered = append(pt.delivered, text)
		return nil
	}
	d.paneProbe = func(context.Context, session.Session) (time.Time, string, error) {
		pt.probes++
		return pt.activity, pt.screen, nil
	}
	if _, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: api.Actor{Node: "home", Key: api.ActorOwner}, To: d.actor(pt.s),
		Body: "hi", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
	return pt
}

func TestDeliverWaitsForActiveClient(t *testing.T) {
	pt := newPaneTest(t)
	pt.activity = time.Now().Add(-2 * time.Second)
	pt.d.orchPass(t.Context())
	if len(pt.delivered) != 0 {
		t.Fatalf("delivered while a person typed: %q", pt.delivered)
	}
	pt.activity = time.Now().Add(-6 * time.Second)
	pt.d.orchPass(t.Context())
	if len(pt.delivered) != 1 {
		t.Fatalf("delivered = %q, want one delivery after the person stopped", pt.delivered)
	}
}

func TestDeliverWaitsForDraft(t *testing.T) {
	pt := newPaneTest(t)
	pt.screen = "────\n❯ half a thought\n────"
	pt.d.orchPass(t.Context())
	if len(pt.delivered) != 0 {
		t.Fatalf("delivered over a draft: %q", pt.delivered)
	}
	if ms := messagesOf(t, pt.d, api.MessageText); len(ms) != 1 || ms[0].State != api.MessageQueued {
		t.Fatalf("messages = %+v", ms)
	}
}

func TestDeliverWithNoClientAndNoDraft(t *testing.T) {
	pt := newPaneTest(t)
	pt.screen = "────\n❯ \n────"
	pt.d.orchPass(t.Context())
	if len(pt.delivered) != 1 {
		t.Fatalf("delivered = %q", pt.delivered)
	}
}

func TestDeliverIgnoresAPIInput(t *testing.T) {
	d, fp := deliverNode(t)
	probes := 0
	d.paneProbe = func(context.Context, session.Session) (time.Time, string, error) {
		probes++
		return time.Now(), "", nil
	}
	a := apiSession(d, "a", session.StatusWorking, nil)
	b := apiSession(d, "b", session.StatusIdle, nil)
	if _, err := d.send(t.Context(), d.localCallerRef(a), b.ID, "hi", ""); err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	if probes != 0 || len(fp.prompts) != 1 {
		t.Fatalf("probes = %d, prompts = %q", probes, fp.prompts)
	}
}

func TestStuckDeliveryDoesNotBlockOthers(t *testing.T) {
	d, _ := deliverNode(t)
	defer func(v time.Duration) { orchCallTimeout = v }(orchCallTimeout)
	orchCallTimeout = 200 * time.Millisecond
	var mu sync.Mutex
	var got []string
	d.deliverFn = func(ctx context.Context, s session.Session, _ string) error {
		if s.AgentSessionID == "x" {
			<-ctx.Done()
			return ctx.Err()
		}
		mu.Lock()
		got = append(got, s.AgentSessionID)
		mu.Unlock()
		return nil
	}
	ids := map[string]string{}
	for _, id := range []string{"x", "y", "z"} {
		s := apiSession(d, id, session.StatusIdle, nil)
		m, err := d.orch.Insert(t.Context(), api.Message{
			Kind: api.MessageText, From: api.Actor{Node: "home", Key: api.ActorOwner}, To: d.actor(s),
			Body: "hi", State: api.MessageQueued,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = m.ID
	}
	start := time.Now()
	d.orchPass(t.Context())
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the pass took %s", took)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"y", "z"}) {
		t.Fatalf("delivered = %v, want y and z", got)
	}
	if st := msgState(t, d, ids["x"]); st != api.MessageQueued {
		t.Fatalf("stuck message state = %s, want queued", st)
	}
	for _, id := range []string{"y", "z"} {
		if st := msgState(t, d, ids[id]); st != api.MessageDelivered {
			t.Fatalf("%s state = %s", id, st)
		}
	}
	if _, held := d.orchHeld["opencode:x"]; !held {
		t.Fatal("the timed-out recipient is not held")
	}
}

func TestCallWithinReturnsOnTimeout(t *testing.T) {
	start := time.Now()
	_, err := callWithin(t.Context(), 50*time.Millisecond, func(ctx context.Context) (int, error) {
		select {}
	})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
	v, err := callWithin(t.Context(), time.Second, func(context.Context) (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Fatalf("v = %d err = %v", v, err)
	}
}

func TestHoldStartsWhenTheDeliveryEnds(t *testing.T) {
	d, _ := deliverNode(t)
	d.deliverFn = func(context.Context, session.Session, string) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}
	s := apiSession(d, "x", session.StatusIdle, nil)
	if _, err := d.orch.Insert(t.Context(), api.Message{
		Kind: api.MessageText, From: api.Actor{Node: "home", Key: api.ActorOwner}, To: d.actor(s),
		Body: "hi", State: api.MessageQueued,
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	d.orchPass(t.Context())
	// The delivery took 200 ms, so a hold that starts when it ends lasts past
	// start + 200 ms + orchDeliverHold. One that starts with the pass does not.
	until, held := d.orchHeld["opencode:x"]
	if !held || until.Before(start.Add(200*time.Millisecond+orchDeliverHold)) {
		t.Fatalf("held until %s, want at least %s after the delivery ended", until.Format(time.StampMilli), orchDeliverHold)
	}
}

func TestProbeAndDeliveryShareOneDeadline(t *testing.T) {
	pt := newPaneTest(t)
	defer func(v time.Duration) { orchCallTimeout = v }(orchCallTimeout)
	orchCallTimeout = 200 * time.Millisecond
	pt.d.paneProbe = func(ctx context.Context, _ session.Session) (time.Time, string, error) {
		<-ctx.Done()
		return time.Time{}, "", ctx.Err()
	}
	start := time.Now()
	pt.d.orchPass(t.Context())
	if took := time.Since(start); took > 350*time.Millisecond {
		t.Fatalf("the pass took %s, want one deadline for probe and delivery", took)
	}
	if len(pt.delivered) != 0 {
		t.Fatalf("delivered after the deadline: %q", pt.delivered)
	}
}
