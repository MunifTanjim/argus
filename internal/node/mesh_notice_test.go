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
	meshPrompter
	got []api.RespondParams
	err error
}

func (f *noticeResponder) Respond(_ context.Context, _ session.Session, p api.RespondParams) error {
	f.got = append(f.got, p)
	return f.err
}

func noticeNode(t *testing.T) (*Node, *fakePrompter, *noticeResponder) {
	t.Helper()
	d, _ := meshNode(t)
	d.id = "home"
	d.adapterList = adapters.All()
	d.reg.SetInputPolicy("claude", nil)
	parent := &fakePrompter{}
	d.adapters["claude"] = meshPrompter{parent}
	resp := &noticeResponder{meshPrompter: meshPrompter{&fakePrompter{}}}
	d.adapters["opencode"] = resp
	if err := d.mesh.PutLink(t.Context(), api.Link{
		Child:  api.Party{Node: "home", Key: "opencode:c"},
		Parent: api.Party{Node: "home", Key: "claude:p1"},
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
	all, err := d.mesh.List(t.Context(), 100)
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
	ns := messagesOf(t, d, api.MessagePromptNotice)
	if len(ns) != 1 {
		t.Fatalf("prompt notices = %d", len(ns))
	}
	return ns[0]
}

func TestPromptNoticeCreatedOnce(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, permission("go test ./..."))
	d.meshPass(t.Context())
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if n.To.Key != "claude:p1" || n.From.Key != api.PartyArgus || n.Subject != "opencode:c" || n.State != api.MessageQueued {
		t.Fatalf("notice = %+v", n)
	}
	if !strings.Contains(n.Body, "go test ./...") || !strings.Contains(n.Body, "argus session respond "+n.ID) {
		t.Fatalf("body = %q", n.Body)
	}
}

func TestRespondByParentAnswers(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	ix := permission("go test ./...")
	ix.RequestID = "r1"
	child := apiSession(d, "c", session.StatusAwaitingInput, ix)
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	if len(resp.got) != 1 || resp.got[0].SessionID != child.ID || resp.got[0].Behavior != "allow" || resp.got[0].RequestID != "r1" {
		t.Fatalf("responder got %+v", resp.got)
	}
	got, _, _ := d.mesh.Get(t.Context(), n.ID)
	if got.State != api.MessageReplied || got.AnsweredBy != "claude:p1" {
		t.Fatalf("notice = %+v", got)
	}
}

func TestRespondRejectsAnswersThatDoNotFit(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	ix := permission("ls")
	ix.Options = []session.DecisionOption{{Label: "Yes", Value: "allow"}, {Label: "No", Value: "deny"}}
	apiSession(d, "c", session.StatusAwaitingInput, ix)
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	for _, tc := range []struct {
		p    api.AgentRespondParams
		want string
	}{
		{api.AgentRespondParams{}, "exactly one"},
		{api.AgentRespondParams{Behavior: "allow", OptionValue: "allow"}, "exactly one"},
		{api.AgentRespondParams{Behavior: "maybe"}, "allow or deny"},
		{api.AgentRespondParams{OptionValue: "bypassPermissions"}, "not an option"},
		{api.AgentRespondParams{Answers: map[string]any{"Q": "A"}}, "only to a question"},
	} {
		tc.p.NoticeID = n.ID
		if err := d.respond(t.Context(), p, tc.p); err == nil || !strings.Contains(err.Error(), tc.want) {
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
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"}); err == nil || !strings.Contains(err.Error(), "decline it with deny") {
		t.Fatalf("allow: err = %v", err)
	}
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Answers: map[string]any{"Other?": "A"}}); err == nil || !strings.Contains(err.Error(), "not a question") {
		t.Fatalf("unknown question: err = %v", err)
	}
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Answers: map[string]any{"Which?": "B"}}); err != nil {
		t.Fatal(err)
	}
	if len(resp.got) != 1 || resp.got[0].Answers["Which?"] != "B" {
		t.Fatalf("responder got %+v", resp.got)
	}
}

func TestRespondByParentNoAnsweredNotice(t *testing.T) {
	d, _, _ := noticeNode(t)
	p := parentSession(d, session.StatusIdle)
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	d.meshPass(t.Context())
	if got := messagesOf(t, d, api.MessageAnsweredNotice); len(got) != 0 {
		t.Fatalf("answered notices = %+v", got)
	}
	if st := msgState(t, d, n.ID); st != api.MessageReplied {
		t.Fatalf("state = %s", st)
	}
}

func TestNoRepeatNoticeWhileAnswerApplies(t *testing.T) {
	d, _, _ := noticeNode(t)
	d.adapters["opencode"] = meshPrompter{&fakePrompter{}}
	p := parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	pd, cancel := d.park(child.ID)
	defer cancel()
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	<-pd.ch
	d.meshPass(t.Context())
	onlyNotice(t, d)
}

func TestRespondFailureReopensNotice(t *testing.T) {
	d, _, resp := noticeNode(t)
	resp.err = errors.New("boom")
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	got, _, _ := d.mesh.Get(t.Context(), n.ID)
	if got.State != api.MessageQueued || got.AnsweredBy != "" {
		t.Fatalf("notice = %+v", got)
	}
}

func TestRespondAfterOwnerAnsweredFails(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	d.reg.ClearInteraction(child.ID)
	err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"})
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
	child := apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	other := apiSession(d, "o", session.StatusIdle, nil)
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	err := d.respond(t.Context(), other, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"})
	if err == nil || err.Error() != "only the parent of home:"+child.ID+" can answer its prompts" {
		t.Fatalf("err = %v", err)
	}
	if len(resp.got) != 0 {
		t.Fatal("responder called")
	}
}

func TestRespondPlanOption(t *testing.T) {
	d, _, resp := noticeNode(t)
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, &session.Interaction{
		Kind: session.InteractionPlan, Plan: "1. do it",
		Options: []session.DecisionOption{{Label: "Auto", Value: "auto"}, {Label: "No", Value: "deny", Reject: true}},
	})
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if !strings.Contains(n.Body, "1. do it") || !strings.Contains(n.Body, "Auto") || !strings.Contains(n.Body, "auto") {
		t.Fatalf("body = %q", n.Body)
	}
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, OptionValue: "auto"}); err != nil {
		t.Fatal(err)
	}
	if len(resp.got) != 1 || resp.got[0].OptionValue != "auto" {
		t.Fatalf("responder got %+v", resp.got)
	}
}

func TestPromptNoticeWithdrawnWhenAnsweredElsewhere(t *testing.T) {
	d, parent, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	child := apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if n.State != api.MessageQueued {
		t.Fatalf("state = %s", n.State)
	}
	d.reg.ClearInteraction(child.ID)
	d.meshPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageWithdrawn {
		t.Fatalf("state = %s", st)
	}
	parentSession(d, session.StatusIdle)
	d.meshPass(t.Context())
	if parent.called {
		t.Fatalf("delivered %q", parent.gotText)
	}
	if got := messagesOf(t, d, api.MessageAnsweredNotice); len(got) != 0 {
		t.Fatalf("answered notices = %+v", got)
	}
}

func TestAnsweredNoticeAfterDelivery(t *testing.T) {
	d, parent, _ := noticeNode(t)
	parentSession(d, session.StatusIdle)
	child := apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if st := msgState(t, d, n.ID); st != api.MessageDelivered || !parent.called {
		t.Fatalf("state = %s called=%v", st, parent.called)
	}
	d.reg.ClearInteraction(child.ID)
	d.meshPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageWithdrawn {
		t.Fatalf("state = %s", st)
	}
	got := messagesOf(t, d, api.MessageAnsweredNotice)
	if len(got) != 1 || got[0].To.Key != "claude:p1" || !strings.Contains(got[0].Body, "The prompt "+n.ID) ||
		!strings.Contains(got[0].Body, "was answered outside argus.") {
		t.Fatalf("answered notices = %+v", got)
	}
}

func TestPromptNoticeWaitsForGoneParent(t *testing.T) {
	d, parent, _ := noticeNode(t)
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if n.State != api.MessageQueued {
		t.Fatalf("state = %s", n.State)
	}
	parentSession(d, session.StatusIdle)
	d.meshPass(t.Context())
	if st := msgState(t, d, n.ID); st != api.MessageDelivered || !parent.called || !strings.Contains(parent.gotText, n.ID) {
		t.Fatalf("state = %s called=%v", st, parent.called)
	}
}

func insertTask(t *testing.T, d *Node) {
	t.Helper()
	if _, err := d.mesh.Insert(t.Context(), api.Message{
		ID: "m1", Kind: api.MessageTask,
		From: api.Party{Node: "home", Key: "claude:p1"}, To: api.Party{Node: "home", Key: "opencode:c"},
		Body: "build it", State: api.MessageDelivered,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTurnNoticeWhenTaskUnreplied(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	insertTask(t, d)
	apiSession(d, "c", session.StatusWorking, nil)
	d.meshPass(t.Context())
	apiSession(d, "c", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	d.meshPass(t.Context())
	d.meshPass(t.Context())
	got := messagesOf(t, d, api.MessageTurnNotice)
	if len(got) != 1 || got[0].To.Key != "claude:p1" || got[0].From.Key != api.PartyArgus ||
		!strings.Contains(got[0].Body, "m1") || !strings.Contains(got[0].Body, "Last output:") {
		t.Fatalf("turn notices = %+v", got)
	}
}

func TestTurnNoticeWhenWorkingFoldsIntoOnePass(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	insertTask(t, d)
	child := apiSession(d, "c", session.StatusIdle, nil)
	d.meshPass(t.Context())
	d.meshNoteEvent(registry.Event{Session: session.Session{ID: child.ID, Status: session.StatusWorking}})
	d.meshPass(t.Context())
	if got := messagesOf(t, d, api.MessageTurnNotice); len(got) != 1 {
		t.Fatalf("turn notices = %+v", got)
	}
}

func TestNoTurnNoticeAfterReply(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	insertTask(t, d)
	child := apiSession(d, "c", session.StatusWorking, nil)
	d.meshPass(t.Context())
	if _, err := d.reply(t.Context(), child, "m1", "done"); err != nil {
		t.Fatal(err)
	}
	apiSession(d, "c", session.StatusAwaitingInput, &session.Interaction{Kind: session.InteractionIdle})
	d.meshPass(t.Context())
	if got := messagesOf(t, d, api.MessageTurnNotice); len(got) != 0 {
		t.Fatalf("turn notices = %+v", got)
	}
}

func TestHandleAgentRespondMeshDisabled(t *testing.T) {
	d := newNode(nil)
	params, _ := json.Marshal(api.AgentRespondParams{NoticeID: "m-00000000", Behavior: "allow"})
	if _, err := d.handleAgentRespond(t.Context(), params); !errors.Is(err, errMeshDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRespondWithoutAnswerPathFails(t *testing.T) {
	d, _, _ := noticeNode(t)
	d.adapters["opencode"] = meshPrompter{&fakePrompter{}}
	p := parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"})
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
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	d.reg.ReconcileSessions("opencode", nil)
	other := apiSession(d, "o", session.StatusIdle, nil)
	err := d.respond(t.Context(), other, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"})
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
	apiSession(d, "c", session.StatusAwaitingInput, permission("ls"))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if err := d.respond(t.Context(), p, api.AgentRespondParams{NoticeID: n.ID, Behavior: "allow"}); err == nil {
		t.Fatal("respond succeeded")
	}
	got, _, _ := d.mesh.Get(t.Context(), n.ID)
	if got.State != api.MessageDelivered || got.AnsweredBy != "" {
		t.Fatalf("notice = %+v", got)
	}
}

func TestPromptNoticeMentionsRespondTool(t *testing.T) {
	d, _, _ := noticeNode(t)
	parentSession(d, session.StatusWorking)
	apiSession(d, "c", session.StatusAwaitingInput, permission("go test ./..."))
	d.meshPass(t.Context())
	n := onlyNotice(t, d)
	if want := "Answer with the argus `respond` tool (notice_id " + n.ID + ")"; !strings.Contains(n.Body, want) {
		t.Fatalf("body = %q", n.Body)
	}
}
