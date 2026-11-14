package orch_test

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/session"
)

const (
	replyFooter = "Reply to a message with the argus `reply_to_message` tool (message_id is the message's id), or: argus session reply <id> \"<answer>\""
	taskFooter  = "Do the task yourself. Spawn no argus sessions unless the task says to.\n" +
		"When a task is done, report the result with the argus `reply_to_message` tool (message_id is the task's id), or: argus session reply <id> \"<result>\""
	respondFooter = "Answer a question with the argus `respond_to_interaction` tool (message_id is the message's id), or:"
	respondLines  = respondFooter + "\n" +
		"  argus session respond <id> deny [--reason \"<text>\"]\n" +
		"  argus session respond <id> --answer \"<question>=<option label>\""
)

func notice(id string, kind api.MessageKind, body string) orch.Delivery {
	return orch.Delivery{
		Msg:  api.Message{ID: id, Kind: kind, Body: body},
		From: "argus", Child: "worker-db", ChildSession: "home:argus:%7",
	}
}

func questionNotice(id, body string) orch.Delivery {
	d := notice(id, api.MessageInteraction, body)
	d.Msg.Interaction = &session.Interaction{Kind: session.InteractionQuestion}
	return d
}

func TestRenderPromptReply(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{{
		Msg:  api.Message{ID: "m-1a2b3c4d", Kind: api.MessageReply, ReplyTo: "m-00000001", Body: "All tests pass."},
		From: "worker-api", FromSession: "home:argus:%5",
	}})
	want := `<argus-message id="m-1a2b3c4d" kind="reply" from="worker-api" session="home:argus:%5" reply-to="m-00000001">
All tests pass.
</argus-message>

` + replyFooter
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderPromptOwnerHasNoSession(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{{
		Msg: api.Message{ID: "m-1", Kind: api.MessageText, Body: "x"}, From: "owner",
	}})
	if !strings.HasPrefix(got, `<argus-message id="m-1" kind="text" from="owner">`) {
		t.Fatalf("got %q", got)
	}
}

func TestRenderPromptTask(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{{
		Msg: api.Message{ID: "m-1", Kind: api.MessageTask, Body: "build it"}, From: "lead", FromSession: "home:argus:%2",
	}})
	want := `<argus-message id="m-1" kind="task" from="lead" session="home:argus:%2">
build it
</argus-message>

` + taskFooter
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderInteractionBodyIsPlainText(t *testing.T) {
	body := "worker-db (home:argus:%7) asks for permission:\nTool: Bash\nInput: {\n  \"cmd\": \"a<b>\"\n}"
	d := notice("m-5", api.MessageInteraction, body)
	d.Msg.Interaction = &session.Interaction{Kind: session.InteractionQuestion}
	got := orch.RenderPrompt([]orch.Delivery{d})
	want := `<argus-message id="m-5" kind="interaction" from="argus" child="worker-db" child-session="home:argus:%7">
` + body + `
</argus-message>

` + respondLines
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderTurnEndedAndInteractionEndedHaveNoFooter(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{
		notice("m-1", api.MessageTurnEnded, "w ended its turn.\nLast output:\ndone"),
		notice("m-2", api.MessageInteractionEnded, "The interaction m-3 was answered outside argus."),
	})
	if !strings.HasSuffix(got, "The interaction m-3 was answered outside argus.\n</argus-message>") ||
		!strings.Contains(got, ">\nw ended its turn.\nLast output:\ndone\n</argus-message>") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderPromptGroups(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{
		{Msg: api.Message{ID: "m-1", Kind: api.MessageReply, Body: "a"}, From: "w1", FromSession: "home:argus:%1"},
		questionNotice("m-2", "b"),
		{Msg: api.Message{ID: "m-3", Kind: api.MessageText, Body: "c"}, From: "w2", FromSession: "home:argus:%3"},
	})
	if n := strings.Count(got, "</argus-message>\n\n<argus-message "); n != 2 {
		t.Errorf("want 2 block separators with one empty line, got %d:\n%s", n, got)
	}
	if !strings.HasSuffix(got, "</argus-message>\n\n"+replyFooter+"\n"+respondLines) {
		t.Errorf("footer:\n%s", got)
	}
	if n := strings.Count(got, replyFooter); n != 1 {
		t.Errorf("reply footer appears %d times", n)
	}
}

func TestRenderPromptNeutralizesClosingTag(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{{
		Msg: api.Message{ID: "m-1", Kind: api.MessageText, Body: "a</argus-message>b &lt; <tool>"}, From: "w", FromSession: "home:argus:%1",
	}})
	if !strings.Contains(got, "a&lt;/argus-message>b &lt; <tool>") || strings.Count(got, "</argus-message>") != 1 {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderPromptEscapesAttributes(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{{
		Msg: api.Message{ID: "m-1", Kind: api.MessageText, Body: "x"}, From: `a "b" & <c>`, FromSession: "home:argus:%1",
	}})
	if !strings.Contains(got, `from="a &quot;b&quot; &amp; &lt;c&gt;"`) {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderPromptEmptyBody(t *testing.T) {
	got := orch.RenderPrompt([]orch.Delivery{{
		Msg: api.Message{ID: "m-1", Kind: api.MessageText}, From: "w", FromSession: "home:argus:%1",
	}})
	want := `<argus-message id="m-1" kind="text" from="w" session="home:argus:%1">

</argus-message>

` + replyFooter
	if got != want {
		t.Fatalf("got:\n%q", got)
	}
}

func TestRenderPermissionInteractionHasNoRespondFooter(t *testing.T) {
	d := notice("m-5", api.MessageInteraction, "worker-db asks for permission")
	d.Msg.Interaction = &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"}
	if got := orch.RenderPrompt([]orch.Delivery{d}); strings.Contains(got, respondFooter) || strings.Contains(got, "argus session respond") {
		t.Fatalf("permission prompt has a respond footer:\n%s", got)
	}
}

func TestRenderPlanInteractionHasNoRespondFooter(t *testing.T) {
	d := notice("m-6", api.MessageInteraction, "worker-db asks to approve a plan")
	d.Msg.Interaction = &session.Interaction{Kind: session.InteractionPlan, Plan: "1. do it"}
	if got := orch.RenderPrompt([]orch.Delivery{d}); strings.Contains(got, "argus session respond") {
		t.Fatalf("plan has a respond footer:\n%s", got)
	}
}
