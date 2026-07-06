package hookdecision

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestBuildClarifyMessage(t *testing.T) {
	toolInput := json.RawMessage(`{"questions":[{"question":"Pick a DB"},{"question":"Region?"}]}`)

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

	msg = buildClarifyMessage(
		json.RawMessage(`{"questions":[{"question":"Langs"}]}`),
		map[string]any{"Langs": []any{"Go", "Dart"}},
	)
	if !strings.Contains(msg, "  Answer: Go, Dart") {
		t.Errorf("multi-select join missing in:\n%s", msg)
	}
}

func TestFormatDecisionChat(t *testing.T) {
	toolName := "AskUserQuestion"
	toolInput := json.RawMessage(`{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`)
	out := FormatDecision(toolName, toolInput, api.RespondParams{
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

func TestFormatDecision(t *testing.T) {
	out := FormatDecision("Bash", nil, api.RespondParams{Behavior: "deny", Reason: "use rg instead"})
	if !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, "use rg instead") {
		t.Errorf("deny: %s", out)
	}

	out = FormatDecision("Bash", nil, api.RespondParams{Behavior: "allow"})
	if !strings.Contains(out, `"behavior":"allow"`) || strings.Contains(out, "updatedInput") {
		t.Errorf("permission allow: %s", out)
	}

	toolInput := json.RawMessage(`{"questions":[{"question":"Pick","options":[{"label":"A"}]}]}`)
	out = FormatDecision("AskUserQuestion", toolInput, api.RespondParams{
		Behavior: "allow",
		Answers:  map[string]any{"Pick": "A"},
	})
	for _, want := range []string{`"behavior":"allow"`, "updatedInput", `"answers"`, `"Pick":"A"`, `"questions"`} {
		if !strings.Contains(out, want) {
			t.Errorf("askuserquestion: missing %q in %s", want, out)
		}
	}
}

func TestFormatDecisionExitPlanMode(t *testing.T) {
	toolInput := json.RawMessage(`{"plan":"do the thing"}`)
	out := FormatDecision("ExitPlanMode", toolInput, api.RespondParams{OptionValue: "acceptEdits"})
	for _, want := range []string{
		`"behavior":"allow"`,
		`"updatedInput"`,
		`"plan":"do the thing"`, // original tool_input echoed verbatim
		`"type":"setMode"`,
		`"mode":"acceptEdits"`,
		`"destination":"session"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exitplanmode allow: missing %q in %s", want, out)
		}
	}

	// Reject keeps deny + message and injects no updatedInput.
	out = FormatDecision("ExitPlanMode", toolInput, api.RespondParams{OptionValue: "deny", Reason: "rethink"})
	if !strings.Contains(out, `"behavior":"deny"`) || !strings.Contains(out, "rethink") {
		t.Errorf("exitplanmode deny: %s", out)
	}
	if strings.Contains(out, "updatedInput") || strings.Contains(out, "updatedPermissions") {
		t.Errorf("exitplanmode deny: should not carry updatedInput/updatedPermissions in %s", out)
	}
}
