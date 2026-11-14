package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type noticeResponder struct {
	orchPrompter
	got []api.RespondParams
	err error
}

func (f *noticeResponder) Respond(_ context.Context, _ session.Session, p api.RespondParams) error {
	f.got = append(f.got, p)
	return f.err
}

func noticeNode(t *testing.T) (*Node, *fakePrompter, *noticeResponder) {
	t.Helper()
	d, _ := orchNode(t)
	d.SetIdentity("home", "")
	d.adapterList = adapters.All()
	d.reg.SetInputPolicy("claude", nil)
	parent := &fakePrompter{}
	d.adapters["claude"] = orchPrompter{parent}
	resp := &noticeResponder{orchPrompter: orchPrompter{&fakePrompter{}}}
	d.adapters["opencode"] = resp
	if err := d.orch.PutLink(t.Context(), api.Link{
		Child:  api.Actor{Node: "home", Key: "opencode:c"},
		Parent: api.Actor{Node: "home", Key: "claude:p1"},
		TaskID: "m1",
	}); err != nil {
		t.Fatal(err)
	}
	return d, parent, resp
}

func parentSession(d *Node, status session.Status) session.Session {
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "p1", Status: status, Input: session.InputAPI,
	})
	return s
}

func permission(input string) *session.Interaction {
	return &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: input}
}

func messagesOf(t *testing.T, d *Node, kind api.MessageKind) []api.Message {
	t.Helper()
	all, err := d.orch.List(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Message
	for _, m := range all {
		if m.Kind == kind {
			out = append(out, m)
		}
	}
	return out
}

func onlyNotice(t *testing.T, d *Node) api.Message {
	t.Helper()
	ns := messagesOf(t, d, api.MessageInteraction)
	if len(ns) != 1 {
		t.Fatalf("prompt notices = %d", len(ns))
	}
	return ns[0]
}

func TestInteractionCreatedOnce(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("go test ./..."))
	d.orchPass(t.Context())
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if n.To.Key != "claude:p1" || n.From.Key != api.ActorArgus || n.Child == nil || *n.Child != (api.Actor{Node: "home", Key: "opencode:c"}) || n.State != api.MessageQueued {
		t.Fatalf("notice = %+v", n)
	}
	if !strings.Contains(n.Body, "go test ./...") {
		t.Fatalf("body = %q", n.Body)
	}
}

func TestRespondByParentAnswers(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	ix := approvable("go test ./...")
	ix.RequestID = "r1"
	child := apiSession(d, "c", session.StatusAwaitingInput, ix)
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed}); err != nil {
		t.Fatal(err)
	}
	if len(resp.got) != 1 || resp.got[0].SessionID != child.ID || resp.got[0].Answers["Proceed?"] != "Yes" || resp.got[0].RequestID != "r1" {
		t.Fatalf("responder got %+v", resp.got)
	}
	got, _, _ := d.orch.Get(t.Context(), n.ID)
	if got.State != api.MessageReplied || got.AnsweredBy == nil || *got.AnsweredBy != (api.Actor{Node: "home", Key: "claude:p1"}) {
		t.Fatalf("notice = %+v", got)
	}
}

func TestRespondRejectsAnswersThatDoNotFit(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	ix := approvable("ls")
	ix.Options = []session.DecisionOption{{Label: "Yes", Value: "allow"}, {Label: "No", Value: "deny"}}
	apiSession(d, "c", session.StatusAwaitingInput, ix)
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	for _, tc := range []struct {
		p    api.OrchRespondParams
		want string
	}{
		{api.OrchRespondParams{}, "exactly one"},
		{api.OrchRespondParams{Behavior: "deny", Answers: proceed}, "exactly one"},
		{api.OrchRespondParams{Behavior: "allow"}, "decline it with deny"},
		{api.OrchRespondParams{Behavior: "maybe"}, "behavior must be deny"},
		{api.OrchRespondParams{Answers: map[string]any{"Q": "A"}}, "is not a question of this interaction"},
	} {
		tc.p.MessageID = n.ID
		if err := d.respond(t.Context(), d.localCallerRef(p), tc.p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.p, err, tc.want)
		}
	}
	if len(resp.got) != 0 {
		t.Fatalf("responder got %+v", resp.got)
	}
	if st := msgState(t, d, n.ID); st != api.MessageQueued {
		t.Fatalf("state = %s", st)
	}
}

func TestRespondChecksQuestionAnswers(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, &session.Interaction{
		Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{{Question: "Which?", Options: []string{"A", "B"}}},
	})
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Behavior: "allow"}); err == nil || !strings.Contains(err.Error(), "decline it with deny") {
		t.Fatalf("allow: err = %v", err)
	}
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: map[string]any{"Other?": "A"}}); err == nil || !strings.Contains(err.Error(), "is not a question of this interaction") {
		t.Fatalf("unknown question: err = %v", err)
	}
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: map[string]any{"Which?": "B"}}); err != nil {
		t.Fatal(err)
	}
	if len(resp.got) != 1 || resp.got[0].Answers["Which?"] != "B" {
		t.Fatalf("responder got %+v", resp.got)
	}
}

func TestRespondByParentNoInteractionEnded(t *testing.T) {
	d, _, _ := noticeNode(t)
	p := parentSession(d, session.StatusIdle)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed}); err != nil {
		t.Fatal(err)
	}
	d.orchPass(t.Context())
	if got := messagesOf(t, d, api.MessageInteractionEnded); len(got) != 0 {
		t.Fatalf("answered notices = %+v", got)
	}
	if st := msgState(t, d, n.ID); st != api.MessageReplied {
		t.Fatalf("state = %s", st)
	}
}

func TestNoRepeatNoticeWhileAnswerApplies(t *testing.T) {
	d, _, _ := noticeNode(t)
	d.adapters["opencode"] = orchPrompter{&fakePrompter{}}
	p := parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	pd := &pendingDecision{id: child.Interaction.RequestID, sessionID: child.ID, ch: make(chan api.RespondParams, 1)}
	d.park(pd)
	defer d.unpark(pd)
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed}); err != nil {
		t.Fatal(err)
	}
	<-pd.ch
	d.orchPass(t.Context())
	onlyNotice(t, d)
}

func TestRespondFailureReopensNotice(t *testing.T) {
	d, _, resp := noticeNode(t)
	resp.err = errors.New("boom")
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	got, _, _ := d.orch.Get(t.Context(), n.ID)
	if got.State != api.MessageQueued || got.AnsweredBy != nil {
		t.Fatalf("notice = %+v", got)
	}
}

func TestRespondAfterOwnerAnsweredFails(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	d.reg.ClearInteraction(child.ID)
	err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed})
	if err == nil || err.Error() != n.ID+" is already answered" {
		t.Fatalf("err = %v", err)
	}
	if len(resp.got) != 0 {
		t.Fatal("responder called")
	}
}

func TestRespondByNonParentFails(t *testing.T) {
	d, _, resp := noticeNode(t)
	parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	other := apiSession(d, "o", session.StatusIdle, nil)
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	err := d.respond(t.Context(), d.localCallerRef(other), api.OrchRespondParams{MessageID: n.ID, Answers: proceed})
	if err == nil || err.Error() != "only the parent of home:"+child.ID+" can answer its prompts" {
		t.Fatalf("err = %v", err)
	}
	if len(resp.got) != 0 {
		t.Fatal("responder called")
	}
}

func TestInteractionWithdrawnWhenAnsweredElsewhere(t *testing.T) {
	d, parent, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if n.State != api.MessageQueued {
		t.Fatalf("state = %s", n.State)
	}
	d.reg.ClearInteraction(child.ID)
	d.orchPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageWithdrawn {
		t.Fatalf("state = %s", st)
	}
	parentSession(d, session.StatusIdle)
	d.orchPass(t.Context())
	if parent.called {
		t.Fatalf("delivered %q", parent.gotText)
	}
	if got := messagesOf(t, d, api.MessageInteractionEnded); len(got) != 0 {
		t.Fatalf("answered notices = %+v", got)
	}
}

func TestInteractionEndedAfterDelivery(t *testing.T) {
	d, parent, _ := noticeNode(t)
	parentSession(d, session.StatusIdle)
	child := apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if st := msgState(t, d, n.ID); st != api.MessageDelivered || !parent.called {
		t.Fatalf("state = %s called=%v", st, parent.called)
	}
	d.reg.ClearInteraction(child.ID)
	d.orchPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageWithdrawn {
		t.Fatalf("state = %s", st)
	}
	got := messagesOf(t, d, api.MessageInteractionEnded)
	if len(got) != 1 || got[0].To.Key != "claude:p1" || !strings.Contains(got[0].Body, "The interaction "+n.ID) ||
		!strings.Contains(got[0].Body, "was answered outside argus.") {
		t.Fatalf("answered notices = %+v", got)
	}
}

func TestInteractionWaitsForGoneParent(t *testing.T) {
	d, parent, _ := noticeNode(t)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if n.State != api.MessageQueued {
		t.Fatalf("state = %s", n.State)
	}
	parentSession(d, session.StatusIdle)
	d.orchPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageDelivered || !parent.called || !strings.Contains(parent.gotText, n.ID) {
		t.Fatalf("state = %s called=%v", st, parent.called)
	}
}

func TestInteractionWaitsWhenParentLeavesAndReturns(t *testing.T) {
	d, parent, resp := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	d.reg.ReconcileSessions("claude", nil)
	d.orchPass(t.Context())
	d.orchPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageQueued {
		t.Fatalf("state after the parent left = %s, want queued", st)
	}
	p := parentSession(d, session.StatusIdle)
	d.orchPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageDelivered || !strings.Contains(parent.gotText, n.ID) {
		t.Fatalf("state = %s text=%q", st, parent.gotText)
	}
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed}); err != nil {
		t.Fatal(err)
	}
	if len(resp.got) != 1 {
		t.Fatalf("responder got %+v", resp.got)
	}
}

func insertTask(t *testing.T, d *Node) {
	t.Helper()
	if _, err := d.orch.Insert(t.Context(), api.Message{
		ID: "m1", Kind: api.MessageTask,
		From: api.Actor{Node: "home", Key: "claude:p1"}, To: api.Actor{Node: "home", Key: "opencode:c"},
		Body: "build it", State: api.MessageDelivered,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTurnEndedWhenTaskUnreplied(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	insertTask(t, d)
	apiSession(d, "c", session.StatusWorking, nil)
	d.orchPass(t.Context())
	apiSession(d, "c", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	d.orchPass(t.Context())
	d.orchPass(t.Context())
	got := messagesOf(t, d, api.MessageTurnEnded)
	if len(got) != 1 || got[0].To.Key != "claude:p1" || got[0].From.Key != api.ActorArgus ||
		!strings.Contains(got[0].Body, "ended its turn without a report on task m1.\nLast output:\n") {
		t.Fatalf("turn notices = %+v", got)
	}
}

func TestTurnEndedWhenWorkingFoldsIntoOnePass(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	insertTask(t, d)
	child := apiSession(d, "c", session.StatusIdle, nil)
	d.orchPass(t.Context())
	d.orchNoteEvent(registry.Event{Session: session.Session{ID: child.ID, Status: session.StatusWorking}})
	d.orchPass(t.Context())
	if got := messagesOf(t, d, api.MessageTurnEnded); len(got) != 1 {
		t.Fatalf("turn notices = %+v", got)
	}
}

func TestNoTurnEndedAfterReply(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	insertTask(t, d)
	child := apiSession(d, "c", session.StatusWorking, nil)
	d.orchPass(t.Context())
	if _, err := d.reply(t.Context(), d.localCallerRef(child), "m1", "done"); err != nil {
		t.Fatal(err)
	}
	apiSession(d, "c", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	d.orchPass(t.Context())
	if got := messagesOf(t, d, api.MessageTurnEnded); len(got) != 0 {
		t.Fatalf("turn notices = %+v", got)
	}
}

func TestHandleAgentRespondOrchDisabled(t *testing.T) {
	d := newNode(nil)
	params, _ := json.Marshal(api.OrchRespondParams{MessageID: "m-00000000", Answers: proceed})
	if _, err := d.handleOrchInteractionRespond(t.Context(), params); !errors.Is(err, errOrchDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRespondWithoutAnswerPathFails(t *testing.T) {
	d, _, _ := noticeNode(t)
	d.adapters["opencode"] = orchPrompter{&fakePrompter{}}
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed})
	if err == nil || err.Error() != n.ID+" cannot be answered through argus" {
		t.Fatalf("err = %v", err)
	}
	if st := msgState(t, d, n.ID); st != api.MessageQueued {
		t.Fatalf("state = %s", st)
	}
}

func TestRespondToGoneChildFails(t *testing.T) {
	d, _, resp := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	d.reg.ReconcileSessions("opencode", nil)
	other := apiSession(d, "o", session.StatusIdle, nil)
	err := d.respond(t.Context(), d.localCallerRef(other), api.OrchRespondParams{MessageID: n.ID, Answers: proceed})
	if err == nil || err.Error() != n.ID+" is already answered" {
		t.Fatalf("err = %v", err)
	}
	if len(resp.got) != 0 {
		t.Fatal("responder called")
	}
}

func TestRespondFailureRestoresDeliveredState(t *testing.T) {
	d, _, resp := noticeNode(t)
	resp.err = errors.New("boom")
	p := parentSession(d, session.StatusIdle)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), d.localCallerRef(p), api.OrchRespondParams{MessageID: n.ID, Answers: proceed}); err == nil {
		t.Fatal("respond succeeded")
	}
	got, _, _ := d.orch.Get(t.Context(), n.ID)
	if got.State != api.MessageDelivered || got.AnsweredBy != nil {
		t.Fatalf("notice = %+v", got)
	}
}

func TestInteractionBodyHasNoHints(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, permission("go test ./..."))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	if strings.Contains(n.Body, "respond") || !strings.HasSuffix(n.Body, "Input: go test ./...\nThe user must answer this in the child's session.") {
		t.Fatalf("body = %q", n.Body)
	}
}

func TestSplitAnswersAtTheQuestion(t *testing.T) {
	ix := &session.Interaction{Kind: session.InteractionQuestion, Questions: []session.QuestionSpec{
		{Question: "Set x=1?"}, {Question: "Other"},
	}}
	cases := map[string]map[string]any{
		"last =":  {"Set x=1?=a": "b"},
		"first =": {"Set x": "1?=a=b"},
		"exact":   {"Set x=1?": "a=b"},
	}
	for name, given := range cases {
		t.Run(name, func(t *testing.T) {
			got := splitAnswers(given, ix)
			if len(got) != 1 || got["Set x=1?"] != "a=b" {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestInteractionBodyShowsMessageAndOptions(t *testing.T) {
	child := session.Session{Agent: "codex", Name: "worker-db"}
	got := renderInteraction(child, "home:argus:%7", &session.Interaction{
		Kind: session.InteractionPermission, Message: "Asks to allow writes under /tmp", ToolName: "Bash",
		Options: []session.DecisionOption{{Label: "Allow for session", Value: "acceptForSession"}},
	})
	want := "worker-db (home:argus:%7) asks for permission:\nAsks to allow writes under /tmp\nTool: Bash\nThe user must answer this in the child's session."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplyToInteractionNamesRespondTool(t *testing.T) {
	d, _, _ := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, approvable("ls"))
	d.orchPass(t.Context())
	n := onlyNotice(t, d)
	_, err := d.reply(t.Context(), d.localCallerRef(p), n.ID, "yes")
	want := "argus messages cannot be replied to: answer an interaction with the respond_to_interaction tool or argus session respond"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// approvable is a question the parent may answer: "Proceed?" with Yes or No,
// about the action text.
func approvable(text string) *session.Interaction {
	return &session.Interaction{Kind: session.InteractionQuestion, Message: text, Questions: []session.QuestionSpec{{Question: "Proceed?", Options: []string{"Yes", "No"}}}}
}

// proceed answers an approvable question with Yes.
var proceed = map[string]any{"Proceed?": "Yes"}

func planInteraction(opts ...session.DecisionOption) *session.Interaction {
	return &session.Interaction{Kind: session.InteractionPlan, Plan: "1. do it", Options: opts}
}

func TestRespondRejectsUserPrompts(t *testing.T) {
	for name, ix := range map[string]*session.Interaction{
		"permission": permission("rm -rf build"),
		"plan":       planInteraction(session.DecisionOption{Label: "Yes", Value: "default"}, session.DecisionOption{Label: "No", Value: "deny", Reject: true}),
	} {
		d, _, resp := noticeNode(t)
		p := parentSession(d, session.StatusWorking)
		apiSession(d, "c", session.StatusAwaitingInput, ix)
		d.orchPass(t.Context())
		n := onlyNotice(t, d)
		for _, form := range []api.OrchRespondParams{{Behavior: "deny"}, {Answers: proceed}} {
			form.MessageID = n.ID
			if err := d.respond(t.Context(), d.localCallerRef(p), form); err == nil || !strings.Contains(err.Error(), "only the user can answer it") {
				t.Errorf("%s %+v: err = %v", name, form, err)
			}
		}
		if len(resp.got) != 0 {
			t.Fatalf("%s: responder got %+v", name, resp.got)
		}
		if !strings.Contains(n.Body, "The user must answer this in the child's session.") || strings.Contains(n.Body, "Options:") {
			t.Fatalf("%s: body = %q", name, n.Body)
		}
	}
}

func TestPermissionInteractionBodySaysUserAnswers(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.orchPass(t.Context())
	if n := onlyNotice(t, d); !strings.Contains(n.Body, "The user must answer this in the child's session.") {
		t.Fatalf("body = %q", n.Body)
	}
}
