package codex

import (
	"context"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func approvalParams(thread string) map[string]any {
	return map[string]any{
		"threadId": thread, "turnId": "u1", "itemId": "i1", "reason": "May I?",
		"command": "touch f", "cwd": "/w", "availableDecisions": []string{"accept", "cancel"},
	}
}

func TestApprovalShowsAndRespondAccepts(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })

	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}})
	f.request(0, "item/commandExecution/requestApproval", approvalParams("t1"))
	s := waitSession(t, reg, "t1", func(s session.Session) bool {
		return s.Status == session.StatusAwaitingInput && s.Interaction != nil && s.Interaction.Kind == session.InteractionPermission
	})
	if s.Interaction.Message != "May I?" {
		t.Fatalf("interaction = %+v", s.Interaction)
	}

	if err := d.respond(context.Background(), s, api.RespondParams{OptionValue: "allow"}); err != nil {
		t.Fatal(err)
	}
	got := f.expect(t, "")
	if string(got.ID) != "0" || string(got.Result) != `{"decision":"accept"}` {
		t.Fatalf("reply = id %s result %s", got.ID, got.Result)
	}
	d.mu.Lock()
	q := d.pending["t1"]
	marked := len(q) == 1 && q[0].answered
	d.mu.Unlock()
	if !marked {
		t.Fatalf("pending after respond = %d entries; want 1 marked answered", len(q))
	}

	// respond already shows Working; the resolved notice drops the answered request.
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking && s.Interaction == nil })
	f.push("serverRequest/resolved", map[string]any{"threadId": "t1", "requestId": 0})
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.mu.Lock()
		n := len(d.pending["t1"])
		d.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending after resolved = %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResolvedElsewhereClearsPrompt(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	_, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(7, "item/commandExecution/requestApproval", approvalParams("t1"))
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	// The TUI answered: the daemon resolves the request and the thread resumes.
	f.push("serverRequest/resolved", map[string]any{"threadId": "t1", "requestId": 7})
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking && s.Interaction == nil })
}

func TestStatusLeavingWaitClearsPrompt(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	_, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(3, "item/commandExecution/requestApproval", approvalParams("t1"))
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking && s.Interaction == nil })
}

func TestSubagentApprovalShowsOnRoot(t *testing.T) {
	child := threadJSON("c1", "active")
	child["parentThreadId"] = "t1"
	st := &daemonState{
		loaded:  []string{"t1", "c1"},
		threads: map[string]map[string]any{"t1": threadJSON("t1", "active"), "c1": child},
	}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.expect(t, "thread/resume")
	f.expect(t, "thread/resume")

	f.request(1, "item/commandExecution/requestApproval", approvalParams("c1"))
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	// The root keeps working while the child waits: the prompt must stay.
	f.push("thread/status/changed", map[string]any{"threadId": "t1", "status": map[string]any{"type": "active", "activeFlags": []string{}}})
	time.Sleep(100 * time.Millisecond)
	s, _ := reg.Get(registry.AgentSessionKey(Agent, "t1"))
	if s.Status != session.StatusAwaitingInput {
		t.Fatalf("root status = %v; want awaiting input while the child's request is open", s.Status)
	}
	if err := d.respond(context.Background(), s, api.RespondParams{Behavior: "deny"}); err != nil {
		t.Fatal(err)
	}
	if got := f.expect(t, ""); string(got.Result) != `{"decision":"decline"}` {
		t.Fatalf("reply = %s", got.Result)
	}
}

func TestReplayedRequestNotDuplicated(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(0, "item/commandExecution/requestApproval", approvalParams("t1"))
	f.request(0, "item/commandExecution/requestApproval", approvalParams("t1"))
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })
	time.Sleep(50 * time.Millisecond)
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := len(d.pending["t1"]); n != 1 {
		t.Fatalf("pending = %d; want 1", n)
	}
}

func TestRespondWithoutPendingErrors(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "idle")}}
	d, _, reg := startDiscoverer(t, st)
	s := waitSession(t, reg, "t1", func(session.Session) bool { return true })
	if err := d.respond(context.Background(), s, api.RespondParams{OptionValue: "allow"}); err == nil {
		t.Fatal("respond with nothing pending must error")
	}
}

func TestSecondQueuedRequestShownAfterResolve(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(1, "item/commandExecution/requestApproval", approvalParams("t1"))
	second := approvalParams("t1")
	second["reason"] = "Second?"
	f.request(2, "item/commandExecution/requestApproval", second)
	s := waitSession(t, reg, "t1", func(s session.Session) bool {
		return s.Interaction != nil && s.Interaction.Message == "May I?"
	})
	time.Sleep(50 * time.Millisecond)

	if err := d.respond(context.Background(), s, api.RespondParams{OptionValue: "allow"}); err != nil {
		t.Fatal(err)
	}
	if got := f.expect(t, ""); string(got.ID) != "1" {
		t.Fatalf("reply id = %s", got.ID)
	}
	f.push("serverRequest/resolved", map[string]any{"threadId": "t1", "requestId": 1})
	waitSession(t, reg, "t1", func(s session.Session) bool {
		return s.Status == session.StatusAwaitingInput && s.Interaction != nil && s.Interaction.Message == "Second?"
	})
}

func TestChildClosedDropsItsRequests(t *testing.T) {
	child := threadJSON("c1", "active")
	child["parentThreadId"] = "t1"
	st := &daemonState{
		loaded:  []string{"t1", "c1"},
		threads: map[string]map[string]any{"t1": threadJSON("t1", "active"), "c1": child},
	}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.expect(t, "thread/resume")
	f.expect(t, "thread/resume")
	f.request(1, "item/commandExecution/requestApproval", approvalParams("c1"))
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusAwaitingInput })

	f.push("thread/closed", map[string]any{"threadId": "c1"})
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking && s.Interaction == nil })
	d.mu.Lock()
	n := len(d.pending["t1"])
	d.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestRespondShowsNextQueuedRequest(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(1, "item/commandExecution/requestApproval", approvalParams("t1"))
	second := approvalParams("t1")
	second["reason"] = "Second?"
	f.request(2, "item/commandExecution/requestApproval", second)
	s := waitSession(t, reg, "t1", func(s session.Session) bool {
		return s.Interaction != nil && s.Interaction.Message == "May I?"
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.mu.Lock()
		n := len(d.pending["t1"])
		d.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending = %d; want 2", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := d.respond(context.Background(), s, api.RespondParams{OptionValue: "allow"}); err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Get(registry.AgentSessionKey(Agent, "t1"))
	if got.Status != session.StatusAwaitingInput || got.Interaction == nil || got.Interaction.Message != "Second?" {
		t.Fatalf("card after respond = %v %+v; want the second request", got.Status, got.Interaction)
	}
}

func TestRespondRejectsRequestFromOldConnection(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, _, reg := startDiscoverer(t, st)
	s := waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	pr := &pendingRequest{id: []byte("5"), method: "item/commandExecution/requestApproval", threadID: "t1", conn: &rpcConn{},
		interaction: &session.Interaction{Kind: session.InteractionPermission, Options: decisionOptions(false)}}
	d.mu.Lock()
	d.pending["t1"] = []*pendingRequest{pr}
	d.mu.Unlock()
	if err := d.respond(context.Background(), s, api.RespondParams{OptionValue: "allow"}); err == nil {
		t.Fatal("respond to a request from a previous connection must error")
	}
	d.mu.Lock()
	answered := pr.answered
	d.mu.Unlock()
	if answered {
		t.Fatal("failed respond must leave the request unanswered")
	}
}

func questionParams(thread string) map[string]any {
	return map[string]any{
		"threadId": thread, "turnId": "u1", "itemId": "i1",
		"questions": []map[string]any{{"id": "db", "header": "DB", "question": "Which db?", "isOther": false,
			"options": []map[string]any{{"label": "Postgres", "description": ""}, {"label": "SQLite", "description": ""}}}},
	}
}

func startQuestion(t *testing.T) (*discoverer, *fakeDaemon, *registry.Registry, session.Session) {
	t.Helper()
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}, turn: map[string]string{"t1": "u1"}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(3, "item/tool/requestUserInput", questionParams("t1"))
	s := waitSession(t, reg, "t1", func(s session.Session) bool {
		return s.Interaction != nil && s.Interaction.Kind == session.InteractionQuestion
	})
	return d, f, reg, s
}

func TestQuestionCancelInterruptsTurn(t *testing.T) {
	d, f, _, s := startQuestion(t)
	if !s.Interaction.CancelInterrupts || !s.Interaction.AllowUnanswered {
		t.Fatal("codex questions interrupt on cancel and accept unanswered submits")
	}
	// The interrupted turn's queued requests end with it.
	f.request(4, "item/tool/requestUserInput", questionParams("t1"))
	waitPending(t, d, "t1", 2)
	if err := d.respond(context.Background(), s, api.RespondParams{Kind: "question", QuestionAction: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if m := f.expect(t, "turn/interrupt"); string(m.Params) != `{"threadId":"t1","turnId":"u1"}` {
		t.Fatalf("interrupt params = %s", m.Params)
	}
	d.mu.Lock()
	n := len(d.pending["t1"])
	d.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending after cancel = %d; want 0", n)
	}
}

func waitPending(t *testing.T, d *discoverer, root string, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.mu.Lock()
		got := len(d.pending[root])
		d.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending[%s] = %d; want %d", root, got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRespondByRequestIDAnswersThatRequest(t *testing.T) {
	st := &daemonState{loaded: []string{"t1"}, threads: map[string]map[string]any{"t1": threadJSON("t1", "active")}}
	d, f, reg := startDiscoverer(t, st)
	waitSession(t, reg, "t1", func(s session.Session) bool { return s.Status == session.StatusWorking })
	f.request(1, "item/commandExecution/requestApproval", approvalParams("t1"))
	s := waitSession(t, reg, "t1", func(s session.Session) bool { return s.Interaction != nil && s.Interaction.RequestID != "" })
	first := s.Interaction.RequestID
	second := approvalParams("t1")
	second["reason"] = "Second?"
	f.request(2, "item/commandExecution/requestApproval", second)
	waitPending(t, d, "t1", 2)

	// The first request is answered elsewhere; a respond to it must not reach the second.
	f.push("serverRequest/resolved", map[string]any{"threadId": "t1", "requestId": 1})
	waitPending(t, d, "t1", 1)
	err := d.respond(context.Background(), s, api.RespondParams{Kind: "permission", RequestID: first, OptionValue: "allow"})
	if err == nil {
		t.Fatal("respond naming a resolved request must error")
	}
	d.mu.Lock()
	answered := d.pending["t1"][0].answered
	d.mu.Unlock()
	if answered {
		t.Fatal("the next request must stay unanswered")
	}
}

func TestRespondRejectsMismatchedAnswers(t *testing.T) {
	approval := &pendingRequest{interaction: &session.Interaction{Kind: session.InteractionPermission, Options: decisionOptions(false)}}
	question := &pendingRequest{interaction: &session.Interaction{Kind: session.InteractionQuestion}}
	for name, c := range map[string]struct {
		pr *pendingRequest
		p  api.RespondParams
	}{
		"kind differs":           {approval, api.RespondParams{Kind: "question", OptionValue: "allow"}},
		"answers to an approval": {approval, api.RespondParams{Behavior: "allow", Answers: map[string]any{"q": "a"}}},
		"cancel to an approval":  {approval, api.RespondParams{QuestionAction: "cancel"}},
		"option not offered":     {approval, api.RespondParams{OptionValue: "acceptForSession"}},
		"no decision":            {approval, api.RespondParams{}},
		"chat on a question":     {question, api.RespondParams{Kind: "question", QuestionAction: "chat"}},
		"option to a question":   {question, api.RespondParams{OptionValue: "allow"}},
	} {
		if err := checkRespond(c.pr, c.p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, c := range map[string]struct {
		pr *pendingRequest
		p  api.RespondParams
	}{
		"option":         {approval, api.RespondParams{Kind: "permission", OptionValue: "deny"}},
		"behavior":       {approval, api.RespondParams{Behavior: "allow"}},
		"answers":        {question, api.RespondParams{Kind: "question", Behavior: "allow", Answers: map[string]any{"q": "a"}}},
		"empty question": {question, api.RespondParams{Kind: "question"}},
		"cancel":         {question, api.RespondParams{QuestionAction: "cancel"}},
	} {
		if err := checkRespond(c.pr, c.p); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
