package antigravity

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

const invokeResult = `Created At: 2026-07-05T01:01:24+06:00
Completed At: 2026-07-05T01:01:24+06:00
Created the following subagents:
{
  "conversationId": "acec302c-335c-46b5-b75a-4d7695c26a3c",
  "logAbsoluteUri": "file:///x/brain/acec302c-335c-46b5-b75a-4d7695c26a3c/.system_generated/logs/transcript.jsonl",
  "workspaceUris": ["file:///x"]
}
`

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestSubagentChildID(t *testing.T) {
	if got := subagentChildID(invokeResult); got != "acec302c-335c-46b5-b75a-4d7695c26a3c" {
		t.Fatalf("child id = %q", got)
	}
	if got := subagentChildID("no json here"); got != "" {
		t.Fatalf("want empty for no id, got %q", got)
	}
}

func TestParseSubagentItem(t *testing.T) {
	tr := `{"type":"PLANNER_RESPONSE","source":"MODEL","tool_calls":[{"name":"invoke_subagent","args":{"toolSummary":"Run subagent","Subagents":[{"name":"test_subagent","TypeName":"General"}]}}],"step_index":0}
{"type":"INVOKE_SUBAGENT","source":"MODEL","content":` + jsonQuote(invokeResult) + `,"step_index":1}
`
	chunks, err := parseTranscript(writeLines(t, tr))
	if err != nil {
		t.Fatal(err)
	}
	var sub transcript.Item
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.Kind == transcript.ItemSubagent {
				sub = it
			}
		}
	}
	if sub.Kind != transcript.ItemSubagent {
		t.Fatalf("expected a subagent item, got chunks %+v", chunks)
	}
	if len(sub.Subagents) != 1 || sub.Subagents[0].ID != "acec302c-335c-46b5-b75a-4d7695c26a3c" {
		t.Fatalf("subagent link wrong: %+v", sub.Subagents)
	}
	if got := sub.Subagents[0].Type; got != "General" {
		t.Fatalf("subagent type = %q, want General", got)
	}
	if got := sub.Subagents[0].Name; got != "test_subagent" {
		t.Fatalf("subagent name = %q, want test_subagent", got)
	}
}
