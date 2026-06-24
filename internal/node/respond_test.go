package node

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestBuildClarifyMessage(t *testing.T) {
	toolInput := json.RawMessage(`{"questions":[{"question":"Pick a DB"},{"question":"Region?"}]}`)

	// Answered + unanswered render distinctly.
	msg := buildClarifyMessage(toolInput, map[string]any{"Pick a DB": "Postgres"})
	for _, want := range []string{
		"The user wants to clarify these questions.",
		"Start by asking them what they would like to clarify.",
		"Questions asked:",
		`- "Pick a DB"`,
		"  Answer: Postgres",
		`- "Region?"`,
		"  (No answer provided)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("clarify message missing %q in:\n%s", want, msg)
		}
	}

	// Multi-select answers join with ", " (Go []string and JSON []any both work).
	msg = buildClarifyMessage(
		json.RawMessage(`{"questions":[{"question":"Langs"}]}`),
		map[string]any{"Langs": []any{"Go", "Dart"}},
	)
	if !strings.Contains(msg, "  Answer: Go, Dart") {
		t.Errorf("multi-select join missing in:\n%s", msg)
	}
}

func TestBuildDecisionChat(t *testing.T) {
	pd := &pendingDecision{
		toolName:  "AskUserQuestion",
		toolInput: json.RawMessage(`{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`),
	}
	out := buildDecision(pd, api.RespondParams{
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
	pd := &pendingDecision{toolName: "Bash"}
	out := buildDecision(pd, api.RespondParams{Behavior: "deny", Reason: "use rg instead"})
	if !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, "use rg instead") {
		t.Errorf("deny: %s", out)
	}

	// Plain permission allow: behavior allow, no updatedInput.
	out = buildDecision(pd, api.RespondParams{Behavior: "allow"})
	if !strings.Contains(out, `"behavior":"allow"`) || strings.Contains(out, "updatedInput") {
		t.Errorf("permission allow: %s", out)
	}

	// AskUserQuestion allow injects answers + echoes the original questions.
	q := &pendingDecision{
		toolName:  "AskUserQuestion",
		toolInput: json.RawMessage(`{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`),
	}
	out = buildDecision(q, api.RespondParams{
		Behavior: "allow",
		Answers:  map[string]any{"Pick": "A"},
	})
	for _, want := range []string{`"behavior":"allow"`, "updatedInput", `"answers"`, `"Pick":"A"`, `"questions"`} {
		if !strings.Contains(out, want) {
			t.Errorf("askuserquestion: missing %q in %s", want, out)
		}
	}
}
