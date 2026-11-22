package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func approvalCard() *session.Interaction {
	return &session.Interaction{
		Kind: session.InteractionPermission, ToolName: "Bash", ToolInput: `{"command":"git push --force"}`,
		RequestID: "r1", Classes: []string{"outward", "irreversible"}, StartedBy: "routine",
		Options: []session.DecisionOption{
			{Label: "Allow once", Value: "allow_once"},
			{Label: "Allow in this thread", Value: "allow_thread"},
			{Label: "Always allow", Value: "allow_always"},
			{Label: "Deny", Value: "deny", Reject: true, Placeholder: "Tell the agent why"},
			{Label: "Explain", Value: "explain"},
		},
	}
}

func TestApprovalCardShowsWarningsAndOptions(t *testing.T) {
	m := promptModel(approvalCard())
	body := ansi.Strip(m.dock.promptBody(&ctx{m: &m}))
	for _, want := range []string{
		"sends data out of this machine", "can't be undone", "started by a routine", "git push --force",
		"Allow once", "Allow in this thread", "Always allow", "Deny", "Explain",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q:\n%s", want, body)
		}
	}
	m.client = &failingRespondClient{}
	m, _ = runDock(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m, cmd := runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	cmd()
	cl := m.client.(*failingRespondClient)
	if p := cl.params[len(cl.params)-1].(api.RespondParams); p.OptionValue != "allow_thread" || p.RequestID != "r1" {
		t.Fatalf("respond = %+v", p)
	}
}

func TestApprovalCardAttendedHasNoStartedBy(t *testing.T) {
	ix := approvalCard()
	ix.StartedBy, ix.Classes = "you", nil
	m := promptModel(ix)
	body := ansi.Strip(m.dock.promptBody(&ctx{m: &m}))
	if strings.Contains(body, "started by") || strings.Contains(body, "⚠") {
		t.Errorf("attended ordinary card:\n%s", body)
	}
}

func secretCard() *session.Interaction {
	return &session.Interaction{
		Kind: session.InteractionSecret, SecretName: "GH_TOKEN", Message: "GitHub token for issues", RequestID: "secret:GH_TOKEN",
		Options: []session.DecisionOption{{Label: "Save", Value: "save"}, {Label: "Decline", Value: "decline", Reject: true}},
	}
}

func TestSecretCardMasksValue(t *testing.T) {
	m := promptModel(secretCard())
	cl := &failingRespondClient{}
	m.client = cl
	m = typeKeys(m, "ghp_supersecret")
	res, _ := m.Update(tea.PasteMsg{Content: "X1"})
	m = res.(model)
	body := ansi.Strip(m.dock.promptBody(&ctx{m: &m}))
	for _, want := range []string{"Secret requested · GH_TOKEN", "GitHub token for issues", "$GH_TOKEN", "Save", "Decline"} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "supersecret") || strings.Contains(ansi.Strip(m.View().Content), "supersecret") {
		t.Fatalf("value shown:\n%s", body)
	}
	m, cmd := runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("save sent nothing")
	}
	cmd()
	if p := cl.params[len(cl.params)-1].(api.RespondParams); p.SecretValue != "ghp_supersecretX1" || p.OptionValue != "save" || p.RequestID != "secret:GH_TOKEN" {
		t.Fatalf("respond = %+v", p)
	}
	if m.dock.secret.Value() != "" {
		t.Error("value kept after send")
	}
}

func TestSecretCardEmptyAndDecline(t *testing.T) {
	m := promptModel(secretCard())
	cl := &failingRespondClient{}
	m.client = cl
	m, cmd := runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || !strings.Contains(m.flash, "value") {
		t.Fatalf("empty save: cmd=%v flash=%q", cmd != nil, m.flash)
	}
	m = typeKeys(m, "abc")
	m, _ = runDock(m, tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = runDock(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	cmd()
	if p := cl.params[len(cl.params)-1].(api.RespondParams); p.OptionValue != "decline" || p.SecretValue != "" {
		t.Fatalf("decline = %+v", p)
	}
}

func TestSecretHints(t *testing.T) {
	ix := secretCard()
	if got := ansi.Strip(interactionHint("claude", ix)); got != "needs a secret · GH_TOKEN" {
		t.Errorf("hint = %q", got)
	}
	if got := dockSummary("claude", ix); got != "Secret: GH_TOKEN" {
		t.Errorf("summary = %q", got)
	}
}

func TestThreadDraftSendAndDiscard(t *testing.T) {
	ts := func(n int) string { return threadT0.Add(time.Duration(n) * time.Minute).Format(time.RFC3339Nano) }
	cl := threadClient()
	cl.timeline.Entries = []transcript.Entry{
		{ID: "ev:1", Kind: transcript.EntryArgus, Label: "draft", Detail: "Drafted Bash: git push", Timestamp: ts(0),
			Event: &transcript.ArgusEvent{ID: 1, Kind: "draft", Payload: json.RawMessage(`{"tool":"Bash","command":"git push"}`)}},
		{ID: "ev:2", Kind: transcript.EntryArgus, Label: "draft", Detail: "Drafted Bash: gh pr create", Timestamp: ts(1),
			Event: &transcript.ArgusEvent{ID: 2, Kind: "draft", Payload: json.RawMessage(`{"tool":"Bash","command":"gh pr create"}`)}},
		{ID: "ev:3", Kind: transcript.EntryArgus, Label: "draft.discarded", Detail: "Discarded the draft", Timestamp: ts(2),
			Event: &transcript.ArgusEvent{ID: 3, Kind: "draft.discarded", Payload: json.RawMessage(`{"event_id":2}`)}},
	}
	m := openedThread(t, cl)
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Drafted Bash: git push  a send · x discard") {
		t.Errorf("open draft lacks keys:\n%s", out)
	}
	if strings.Contains(out, "gh pr create  a send") {
		t.Error("a discarded draft still offers send")
	}
	m = typeKeys(m, "k") // the discarded draft
	if mm, cmd := upd(m, keyMsg("a")); cmd != nil || mm.flash != "not an open draft" {
		t.Fatalf("a on a discarded draft: flash %q", mm.flash)
	}
	if !strings.Contains(offerKeys.Start.Help().Desc, "send") || !strings.Contains(offerKeys.Dismiss.Help().Desc, "discard") {
		t.Errorf("key help = %q / %q", offerKeys.Start.Help().Desc, offerKeys.Dismiss.Help().Desc)
	}
	m = typeKeys(m, "k") // the open draft
	m, cmd := upd(m, keyMsg("a"))
	cmd()
	_, cmd = upd(m, keyMsg("x"))
	cmd()
	var got []string
	for i, call := range cl.calls {
		if call == api.MethodPAgentDraftSend || call == api.MethodPAgentDraftDiscard {
			p := cl.params[i].(api.PAgentDraftParams)
			got = append(got, call+" "+p.ThreadID)
			if p.EventID != 1 {
				t.Errorf("event id = %d", p.EventID)
			}
		}
	}
	if strings.Join(got, ",") != api.MethodPAgentDraftSend+" n1:t1,"+api.MethodPAgentDraftDiscard+" n1:t1" {
		t.Fatalf("draft calls = %v", got)
	}
}
