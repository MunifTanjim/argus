package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/policy"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

// approvalRig is a memRig with a live Claude session in thread th, started
// as startedBy.
type approvalRig struct {
	*memRig
	th  string
	sid string
}

func newApprovalRig(t *testing.T, startedBy string) *approvalRig {
	t.Helper()
	r := newMemRig(t, 0)
	ctx := context.Background()
	th := r.th
	if startedBy != startedByYou {
		th = "th_run"
		if err := r.s.InsertThread(ctx, pagent.Thread{ID: th, AgentID: r.a.ID, Kind: "run", StartedBy: startedBy, Title: "nightly", Status: pagent.ThreadActive}); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := r.d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", Server: session.TmuxServerArgus, PaneID: "%1", AgentSessionID: "c1",
		Cwd: r.a.Dir, Status: session.StatusWorking,
	})
	if err := r.s.InsertThreadSession(ctx, pagent.ThreadSession{ThreadID: th, SessionID: s.ID, CLI: "claude", AgentSessionID: "c1", StartedBy: startedBy}); err != nil {
		t.Fatal(err)
	}
	return &approvalRig{memRig: r, th: th, sid: s.ID}
}

func (r *approvalRig) rules(t *testing.T, rules ...policy.Rule) {
	t.Helper()
	def, err := pagent.LoadDefinition(r.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	def.Approvals = rules
	if err := def.Save(r.a.Dir); err != nil {
		t.Fatal(err)
	}
}

func toolPermission(tool string, input any) claudecode.HookEvent {
	raw, _ := json.Marshal(map[string]any{"tool_name": tool, "tool_input": input})
	return claudecode.HookEvent{Event: "PermissionRequest", Payload: raw}
}

func shellPermission(cmd string) claudecode.HookEvent {
	return toolPermission("Bash", map[string]string{"command": cmd})
}

type hookAnswer struct {
	Behavior, Message string
	SetMode           bool
}

func decodeAnswer(t *testing.T, out string) hookAnswer {
	t.Helper()
	var v struct {
		HookSpecificOutput struct {
			Decision struct {
				Behavior           string           `json:"behavior"`
				Message            string           `json:"message"`
				UpdatedPermissions []map[string]any `json:"updatedPermissions"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	dec := v.HookSpecificOutput.Decision
	return hookAnswer{dec.Behavior, dec.Message, len(dec.UpdatedPermissions) > 0}
}

// decideNow runs a request that must be answered without a card.
func (r *approvalRig) decideNow(t *testing.T, ev claudecode.HookEvent) hookAnswer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := r.d.awaitDecision(ctx, r.d.adapterFor(""), r.sid, newID(), ev)
	if out == "" {
		t.Fatal("request parked or timed out: want an immediate answer")
	}
	return decodeAnswer(t, out)
}

func (r *approvalRig) answer(t *testing.T, c parkedCall, p api.RespondParams) hookAnswer {
	t.Helper()
	p.SessionID = r.sid
	if err := respond(t, r.d, p); err != nil {
		t.Fatalf("respond: %v", err)
	}
	return decodeAnswer(t, waitOut(t, c))
}

func waitOut(t *testing.T, c parkedCall) string {
	t.Helper()
	select {
	case out := <-c.out:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
		return ""
	}
}

// TestApprovalNonAgentSessionUnchanged: a session outside any agent thread
// shows the CLI's prompt and gets exactly the answer it got before.
func TestApprovalNonAgentSessionUnchanged(t *testing.T) {
	r := newMemRig(t, 0)
	r.d.pagentCfg.Execution = policy.ExecutionNeverUnattended
	s, _ := r.d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%7", Status: session.StatusWorking})
	ev := shellPermission("git push --force")
	wantIx, _ := claudecode.PermissionPrompt(ev)
	for _, p := range []api.RespondParams{
		{Kind: "permission", Behavior: "allow"},
		{Kind: "permission", OptionValue: "deny", Reason: "no"},
		{Kind: "permission", OptionValue: "acceptEdits"},
	} {
		id := newID()
		c := parkRequest(t, r.d, s.ID, id, ev)
		ix := head(r.d, s.ID)
		want := *wantIx
		want.RequestID = id
		if ix == nil || !jsonEqual(*ix, want) {
			t.Fatalf("interaction = %+v, want %+v", ix, want)
		}
		p.SessionID, p.RequestID = s.ID, id
		if err := respond(t, r.d, p); err != nil {
			t.Fatal(err)
		}
		toolName, toolInput := claudecode.PermissionPayload(ev)
		if got, want := waitOut(t, c), r.d.adapterFor("").FormatDecision(toolName, toolInput, p); got != want {
			t.Errorf("answer %+v = %s, want %s", p, got, want)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestApprovalUnattendedDefaultDenies(t *testing.T) {
	r := newApprovalRig(t, "routine")
	got := r.decideNow(t, shellPermission("ls"))
	if got.Behavior != "deny" || got.Message != policy.ReasonUnattended {
		t.Errorf("answer = %+v", got)
	}
	if ix := head(r.d, r.sid); ix != nil {
		t.Errorf("card shown: %+v", ix)
	}
}

func TestApprovalRuleAllowsAndDenies(t *testing.T) {
	r := newApprovalRig(t, "routine")
	r.rules(t,
		policy.Rule{Tool: "Bash", Target: "ls", Decision: policy.OutcomeAllow},
		policy.Rule{Tool: "Bash", Target: "rm", Decision: policy.OutcomeDeny, Note: "no deleting"},
	)
	if got := r.decideNow(t, shellPermission("ls -la")); got.Behavior != "allow" || got.Message != "" || got.SetMode {
		t.Errorf("allow rule: %+v", got)
	}
	if got := r.decideNow(t, shellPermission("rm x")); got.Behavior != "deny" || got.Message != "no deleting" {
		t.Errorf("deny rule: %+v", got)
	}
}

func TestApprovalGlobalRules(t *testing.T) {
	r := newApprovalRig(t, "routine")
	if err := os.MkdirAll(r.d.pagentCfg.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.d.pagentCfg.ProfileDir, "approvals.yaml"), []byte("approvals:\n  - {tool: Bash, target: ls, decision: allow}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := r.decideNow(t, shellPermission("ls")); got.Behavior != "allow" {
		t.Errorf("global rule: %+v", got)
	}
}

func TestApprovalUnattendedOutwardIsDraft(t *testing.T) {
	r := newApprovalRig(t, "routine")
	r.rules(t, policy.Rule{Tool: "Bash", Target: "git", Decision: policy.OutcomeAllow})
	got := r.decideNow(t, shellPermission("git push origin main"))
	if got.Behavior != "deny" || got.Message != "staged as a draft for the user; do not retry" {
		t.Fatalf("answer = %+v", got)
	}
	ev := r.draftEvents(t)
	if len(ev) != 1 {
		t.Fatalf("draft events = %d", len(ev))
	}
	var p map[string]any
	_ = json.Unmarshal(ev[0].Payload, &p)
	if p["command"] != "git push origin main" || p["tool"] != "Bash" || p["target"] != "git" || ev[0].StartedBy != "routine" {
		t.Errorf("draft = %s by %s", ev[0].Payload, ev[0].StartedBy)
	}
	if cl, _ := p["classes"].([]any); len(cl) != 1 || cl[0] != policy.ClassOutward {
		t.Errorf("classes = %v", p["classes"])
	}
}

func (r *approvalRig) draftEvents(t *testing.T) []pagent.Event {
	t.Helper()
	all, err := r.s.Events(context.Background(), r.th, 0)
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(e pagent.Event) bool { return e.Kind != evDraft })
}

func TestApprovalAskCardAllowInThread(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	ev := shellPermission("git push")
	c := parkRequest(t, r.d, r.sid, "r1", ev)
	ix := head(r.d, r.sid)
	var opts []string
	for _, o := range ix.Options {
		opts = append(opts, o.Value)
	}
	if strings.Join(opts, ",") != "allow_once,allow_thread,allow_always,deny,explain" {
		t.Errorf("options = %v", opts)
	}
	if !slices.Equal(ix.Classes, []string{policy.ClassOutward}) || ix.StartedBy != startedByYou || ix.ToolName != "Bash" {
		t.Errorf("card = %+v", ix)
	}
	if got := r.answer(t, c, api.RespondParams{RequestID: "r1", OptionValue: optAllowThread}); got.Behavior != "allow" || got.SetMode {
		t.Fatalf("answer = %+v", got)
	}
	if got := r.decideNow(t, ev); got.Behavior != "allow" {
		t.Errorf("granted repeat = %+v", got)
	}
	// The grant ends with the thread's session.
	r.d.approvalTrack(context.Background(), registry.Event{Type: registry.EventRemoved, Session: session.Session{ID: r.sid}})
	c = parkRequest(t, r.d, r.sid, "r2", ev)
	if got := r.answer(t, c, api.RespondParams{RequestID: "r2", OptionValue: optAllowOnce}); got.Behavior != "allow" {
		t.Errorf("after end = %+v", got)
	}
}

func TestApprovalDenyRepeatsUntilUserMessage(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	ev := shellPermission("rm -rf build")
	c := parkRequest(t, r.d, r.sid, "r1", ev)
	if got := r.answer(t, c, api.RespondParams{RequestID: "r1", OptionValue: optDeny, Reason: "not now"}); got.Behavior != "deny" || got.Message != "not now" {
		t.Fatalf("deny = %+v", got)
	}
	if got := r.decideNow(t, ev); got.Behavior != "deny" || got.Message != policy.ReasonRepeat {
		t.Errorf("repeat = %+v", got)
	}
	r.d.approvalUserMessage(r.th)
	c = parkRequest(t, r.d, r.sid, "r2", ev)
	if got := r.answer(t, c, api.RespondParams{RequestID: "r2", OptionValue: optExplain}); got.Behavior != "deny" || got.Message != reasonExplain {
		t.Errorf("explain = %+v", got)
	}
}

func TestApprovalUnattendedCardExpires(t *testing.T) {
	r := newApprovalRig(t, "routine")
	r.d.pagentCfg.Approvals.UnattendedExpiry = 50 * time.Millisecond
	r.rules(t, policy.Rule{Tool: "Bash", Target: "make", Decision: policy.OutcomeAsk})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := r.d.awaitDecision(ctx, r.d.adapterFor(""), r.sid, "r1", shellPermission("make"))
	if got := decodeAnswer(t, out); got.Behavior != "deny" || got.Message != reasonNoAnswer {
		t.Fatalf("expired = %+v", got)
	}
	err := respond(t, r.d, api.RespondParams{SessionID: r.sid, RequestID: "r1", OptionValue: optAllowOnce})
	if !errors.Is(err, errRequestExpired) {
		t.Errorf("late answer err = %v", err)
	}
}

func TestApprovalChatCardDoesNotExpire(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	r.d.pagentCfg.Approvals.UnattendedExpiry = 20 * time.Millisecond
	c := parkRequest(t, r.d, r.sid, "r1", shellPermission("git push"))
	time.Sleep(60 * time.Millisecond)
	if got := r.answer(t, c, api.RespondParams{RequestID: "r1", OptionValue: optAllowOnce}); got.Behavior != "allow" {
		t.Errorf("answer = %+v", got)
	}
}

func TestApprovalNewMessageClosesCards(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	c := parkRequest(t, r.d, r.sid, "r1", shellPermission("git push"))
	r.d.approvalUserMessage(r.th)
	if got := decodeAnswer(t, waitOut(t, c)); got.Behavior != "deny" || got.Message != reasonNewMessage {
		t.Errorf("closed = %+v", got)
	}
	if pendingHas(r.d, "r1") {
		t.Error("still parked")
	}
}

func TestApprovalThreadSendClosesCards(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	r.d.deliverFn = func(context.Context, session.Session, string) error { return nil }
	c := parkRequest(t, r.d, r.sid, "r1", shellPermission("git push"))
	go func() {
		_, _ = callPAgent[api.PAgentSendResult](t, r.d.handlePAgentThreadSend, api.PAgentSendParams{AgentID: r.a.ID, ThreadID: r.th, Text: "stop"})
	}()
	if got := decodeAnswer(t, waitOut(t, c)); got.Behavior != "deny" || got.Message != reasonNewMessage {
		t.Errorf("closed = %+v", got)
	}
}

func TestApprovalAlwaysAllowCommitsRule(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	ev := shellPermission("gh pr create --fill")
	c := parkRequest(t, r.d, r.sid, "r1", ev)
	if got := r.answer(t, c, api.RespondParams{RequestID: "r1", OptionValue: optAllowAlways}); got.Behavior != "allow" {
		t.Fatalf("answer = %+v", got)
	}
	def, err := pagent.LoadDefinition(r.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(def.Approvals) != 1 || def.Approvals[0].Tool != "Bash" || def.Approvals[0].Target != "gh" || def.Approvals[0].Decision != "allow" {
		t.Fatalf("approvals = %+v", def.Approvals)
	}
	out, err := exec.Command("git", "-C", r.a.Dir, "log", "-1", "--format=%an%n%B", "--", "agent.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v %s", err, out)
	}
	for _, want := range []string{"Argus", "Argus-Source: you", "Argus-Thread: " + r.th} {
		if !strings.Contains(string(out), want) {
			t.Errorf("commit lacks %q:\n%s", want, out)
		}
	}
	if got := r.decideNow(t, ev); got.Behavior != "allow" {
		t.Errorf("after rule = %+v", got)
	}
}

func TestApprovalQuestionsBypassPolicy(t *testing.T) {
	r := newApprovalRig(t, "routine")
	ev := toolPermission("AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which?", "options": []map[string]string{{"label": "A"}}}}})
	c := parkRequest(t, r.d, r.sid, "r1", ev)
	if ix := head(r.d, r.sid); ix.Kind != session.InteractionQuestion || ix.StartedBy != "" {
		t.Errorf("question = %+v", ix)
	}
	c.cancel()
	waitOut(t, c)
}

func (r *approvalRig) insertDraft(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	raw, _ := json.Marshal(payload)
	id, err := r.s.InsertEvent(context.Background(), pagent.Event{ThreadID: r.th, Kind: evDraft, StartedBy: "routine", Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *approvalRig) waitEvent(t *testing.T, kind string) pagent.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all, _ := r.s.Events(context.Background(), r.th, 0)
		for _, e := range all {
			if e.Kind == kind {
				return e
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s event", kind)
	return pagent.Event{}
}

func TestDraftSendShellRunsInAgentDir(t *testing.T) {
	r := newApprovalRig(t, "routine")
	id := r.insertDraft(t, map[string]any{"tool": "Bash", "target": "echo", "command": "echo sent > sent.txt && echo done", "input": map[string]string{"command": "echo sent > sent.txt && echo done"}})
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftSend, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err != nil {
		t.Fatal(err)
	}
	ev := r.waitEvent(t, evDraftSent)
	var p struct {
		EventID  int64  `json:"event_id"`
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	_ = json.Unmarshal(ev.Payload, &p)
	if p.EventID != id || strings.TrimSpace(p.Output) != "done" || p.ExitCode != 0 {
		t.Errorf("sent = %s", ev.Payload)
	}
	if data, err := os.ReadFile(filepath.Join(r.a.Dir, "sent.txt")); err != nil || strings.TrimSpace(string(data)) != "sent" {
		t.Errorf("sent.txt = %q %v", data, err)
	}
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftSend, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err == nil || !strings.Contains(err.Error(), "already sent") {
		t.Errorf("second send err = %v", err)
	}
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftDiscard, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err == nil {
		t.Error("discard after send succeeded")
	}
}

func TestDraftSendOtherToolMessagesAgent(t *testing.T) {
	r := newApprovalRig(t, startedByYou)
	r.d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", Server: session.TmuxServerArgus, PaneID: "%1", Status: session.StatusIdle})
	var sent []string
	r.d.deliverFn = func(_ context.Context, _ session.Session, text string) error {
		sent = append(sent, text)
		return nil
	}
	id := r.insertDraft(t, map[string]any{"tool": "mcp__slack__post_message", "target": "post_message", "input": map[string]string{"text": "hi"}})
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftSend, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != `The user approved: mcp__slack__post_message with input {"text":"hi"}. Do it now.` {
		t.Errorf("sent = %q", sent)
	}
	r.waitEvent(t, evDraftSent)
}

func TestDraftDiscard(t *testing.T) {
	r := newApprovalRig(t, "routine")
	id := r.insertDraft(t, map[string]any{"tool": "Bash", "command": "true"})
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftDiscard, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err != nil {
		t.Fatal(err)
	}
	r.waitEvent(t, evDraftDiscarded)
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentDraftSend, api.PAgentDraftParams{ThreadID: r.th, EventID: id}); err == nil || !strings.Contains(err.Error(), "discarded") {
		t.Errorf("send after discard err = %v", err)
	}
}

// chanResponder records headless answers.
type chanResponder struct {
	adapter.Adapter
	got chan api.RespondParams
}

func (f *chanResponder) Respond(_ context.Context, _ session.Session, p api.RespondParams) error {
	f.got <- p
	return nil
}

func headlessRig(t *testing.T, startedBy string) (*approvalRig, *chanResponder, session.Session) {
	t.Helper()
	r := newApprovalRig(t, startedBy)
	fr := &chanResponder{Adapter: r.d.adapterFor(""), got: make(chan api.RespondParams, 4)}
	r.d.adapters["opencode"] = fr
	s, _ := r.d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "oc1", Status: session.StatusWorking})
	if err := r.s.InsertThreadSession(context.Background(), pagent.ThreadSession{ThreadID: r.th, SessionID: s.ID, CLI: "opencode", AgentSessionID: "oc1", StartedBy: startedBy}); err != nil {
		t.Fatal(err)
	}
	return r, fr, s
}

func headlessAsk(r *approvalRig, s session.Session, cmd string) session.Session {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	s, _ = r.d.reg.ApplyHook(registry.HookUpdate{Agent: "opencode", AgentSessionID: "oc1", Status: session.StatusAwaitingInput,
		Interaction: &session.Interaction{Kind: session.InteractionPermission, ToolName: "bash", ToolInput: string(in), Options: []session.DecisionOption{
			{Label: "Allow", Value: "allow"}, {Label: "Deny", Value: "deny", Reject: true},
		}}})
	r.d.approvalTrack(context.Background(), registry.Event{Type: registry.EventUpdated, Session: s})
	return s
}

func recv(t *testing.T, ch chan api.RespondParams) api.RespondParams {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no headless answer")
		return api.RespondParams{}
	}
}

func TestApprovalHeadlessAutoAnswers(t *testing.T) {
	r, fr, s := headlessRig(t, "routine")
	headlessAsk(r, s, "ls")
	if p := recv(t, fr.got); p.Behavior != "deny" || p.Reason != policy.ReasonUnattended || p.OptionValue != "" {
		t.Errorf("answer = %+v", p)
	}
}

func TestApprovalHeadlessCard(t *testing.T) {
	r, fr, s := headlessRig(t, startedByYou)
	s = headlessAsk(r, s, "git push")
	shown := r.d.withCaps(s)
	if shown.Interaction == nil || len(shown.Interaction.Options) != 5 || !slices.Equal(shown.Interaction.Classes, []string{policy.ClassOutward}) {
		t.Fatalf("card = %+v", shown.Interaction)
	}
	if err := respond(t, r.d, api.RespondParams{SessionID: s.ID, OptionValue: optAllowThread}); err != nil {
		t.Fatal(err)
	}
	if p := recv(t, fr.got); p.Behavior != "allow" || p.OptionValue != "" {
		t.Errorf("answer = %+v", p)
	}
}

func TestApprovalOptionsForNonAgentNode(t *testing.T) {
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	s, _ := d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", Server: session.TmuxServerDefault, PaneID: "%3", Status: session.StatusWorking})
	c := parkRequest(t, d, s.ID, "r1", shellPermission("git push"))
	if ix := head(d, s.ID); len(ix.Options) != 2 || ix.Classes != nil {
		t.Errorf("card = %+v", ix)
	}
	c.cancel()
	waitOut(t, c)
}
