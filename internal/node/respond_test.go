package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/hookdecision"
	"github.com/MunifTanjim/argus/internal/api"
)

func TestBuildClarifyMessage(t *testing.T) {
	toolInput := json.RawMessage(`{"questions":[{"question":"Pick a DB"},{"question":"Region?"}]}`)

	// Answered + unanswered render distinctly.
	out := hookdecision.FormatDecision("AskUserQuestion", toolInput, api.RespondParams{
		QuestionAction: "chat",
		Answers:        map[string]any{"Pick a DB": "Postgres"},
	})
	for _, want := range []string{
		"The user wants to clarify these questions.",
		"Start by asking them what they would like to clarify.",
		"Questions asked:",
		`\"Pick a DB\"`,
		"Answer: Postgres",
		`\"Region?\"`,
		"(No answer provided)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("clarify message missing %q in:\n%s", want, out)
		}
	}

	// Multi-select answers join with ", " (Go []string and JSON []any both work).
	out = hookdecision.FormatDecision("AskUserQuestion",
		json.RawMessage(`{"questions":[{"question":"Langs"}]}`),
		api.RespondParams{
			QuestionAction: "chat",
			Answers:        map[string]any{"Langs": []any{"Go", "Dart"}},
		},
	)
	if !strings.Contains(out, "Answer: Go, Dart") {
		t.Errorf("multi-select join missing in:\n%s", out)
	}
}

func TestBuildDecisionChat(t *testing.T) {
	toolInput := json.RawMessage(`{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`)
	out := hookdecision.FormatDecision("AskUserQuestion", toolInput, api.RespondParams{
		QuestionAction: "chat",
		Answers:        map[string]any{"Pick": "A"},
	})
	if !strings.Contains(out, `"behavior":"deny"`) {
		t.Errorf("chat: want deny in %s", out)
	}
	if !strings.Contains(out, "clarify these questions") || !strings.Contains(out, `\"Pick\"`) {
		t.Errorf("chat: missing clarify message / question in %s", out)
	}
	if strings.Contains(out, "updatedInput") {
		t.Errorf("chat: should not inject updatedInput in %s", out)
	}
}

func TestBuildDecision(t *testing.T) {
	// Deny carries the reason as the decision message.
	out := hookdecision.FormatDecision("Bash", nil, api.RespondParams{Behavior: "deny", Reason: "use rg instead"})
	if !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, "use rg instead") {
		t.Errorf("deny: %s", out)
	}

	// Plain permission allow: behavior allow, no updatedInput.
	out = hookdecision.FormatDecision("Bash", nil, api.RespondParams{Behavior: "allow"})
	if !strings.Contains(out, `"behavior":"allow"`) || strings.Contains(out, "updatedInput") {
		t.Errorf("permission allow: %s", out)
	}

	// AskUserQuestion allow injects answers + echoes the original questions.
	toolInput := json.RawMessage(`{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`)
	out = hookdecision.FormatDecision("AskUserQuestion", toolInput, api.RespondParams{
		Behavior: "allow",
		Answers:  map[string]any{"Pick": "A"},
	})
	for _, want := range []string{`"behavior":"allow"`, "updatedInput", `"answers"`, `"Pick":"A"`, `"questions"`} {
		if !strings.Contains(out, want) {
			t.Errorf("askuserquestion: missing %q in %s", want, out)
		}
	}
}
