package opencode

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

const messagesFixture = `{"data":[
	{"type":"user","id":"m1","time":{"created":1},"text":"hello","files":[],"agents":[]},
	{"type":"synthetic","id":"m1b","time":{"created":1},"text":"<system-reminder>ignore</system-reminder>"},
	{"type":"assistant","id":"m2","time":{"created":2},"agent":"build","model":{"id":"glm","providerID":"opencode-go","variant":"high"},"content":[
		{"type":"reasoning","id":"p2","text":"thinking"},
		{"type":"text","id":"p3","text":"hi there"},
		{"type":"tool","id":"call_1","name":"read","executed":false,"state":{"status":"completed","input":{"path":"foo.go"},"content":[{"type":"text","text":"file.txt"}]}},
		{"type":"tool","id":"call_2","name":"bash","state":{"status":"error","input":{"command":"nope"},"error":{"type":"aborted","message":"x"}}}
	]}
],"cursor":{"previous":"","next":""}}`

const subagentFixture = `{"data":[
	{"type":"assistant","id":"m1","time":{"created":1},"model":{"id":"glm"},"content":[
		{"type":"tool","id":"call_1","name":"subagent","state":{"status":"completed","input":{"agent":"explore","description":"look at X","prompt":"do it"},"metadata":{"sessionID":"ses_child"}}},
		{"type":"tool","id":"call_2","name":"task","state":{"status":"error","input":{},"error":{"type":"aborted","message":"spawn failed"}}}
	]}
],"cursor":{"previous":"","next":""}}`

func TestFoldMessagesSubagent(t *testing.T) {
	var env ocEnvelope[ocMessage]
	if err := json.Unmarshal([]byte(subagentFixture), &env); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	items := foldMessages(env.Data).Chunks[0].Items

	var sub, failed transcript.Item
	for _, it := range items {
		switch it.ToolName {
		case "subagent":
			sub = it
		case "task":
			failed = it
		}
	}

	// A subagent with a child session id becomes a drillable ItemSubagent.
	if sub.Kind != transcript.ItemSubagent {
		t.Fatalf("subagent kind = %q, want %q", sub.Kind, transcript.ItemSubagent)
	}
	if len(sub.Subagents) != 1 {
		t.Fatalf("want 1 subagent ref, got %d", len(sub.Subagents))
	}
	s := sub.Subagents[0]
	if s.ID != "ses_child" || s.Type != "explore" || s.Desc != "look at X" || !s.HasTrace {
		t.Fatalf("subagent ref = %+v", s)
	}

	// A task that never spawned a child (no metadata.sessionID) stays a plain tool.
	if failed.Kind != transcript.ItemTool {
		t.Fatalf("childless task kind = %q, want %q", failed.Kind, transcript.ItemTool)
	}
}

func TestFoldMessages(t *testing.T) {
	var env ocEnvelope[ocMessage]
	if err := json.Unmarshal([]byte(messagesFixture), &env); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	view := foldMessages(env.Data)
	if len(view.Chunks) != 2 {
		t.Fatalf("want 2 chunks (user, assistant), got %d", len(view.Chunks))
	}
	if view.Chunks[0].Kind != transcript.ChunkUser || view.Chunks[0].Text != "hello" {
		t.Fatalf("user chunk: %+v", view.Chunks[0])
	}
	ai := view.Chunks[1]
	if ai.Kind != transcript.ChunkAI || ai.ModelName != "glm" {
		t.Fatalf("ai chunk: %+v", ai)
	}
	var kinds []transcript.ItemKind
	for _, it := range ai.Items {
		kinds = append(kinds, it.Kind)
	}
	if len(kinds) != 4 || kinds[0] != transcript.ItemThinking || kinds[1] != transcript.ItemText || kinds[2] != transcript.ItemTool || kinds[3] != transcript.ItemTool {
		t.Fatalf("item kinds: %v", kinds)
	}
	var completed, errored transcript.Item
	for _, it := range ai.Items {
		if it.ToolName == "read" {
			completed = it
		}
		if it.ToolName == "bash" {
			errored = it
		}
	}
	if completed.ToolID != "call_1" || completed.Result != "file.txt" || completed.ResultIsError {
		t.Fatalf("completed tool: %+v", completed)
	}
	if completed.ToolInput != `{"path":"foo.go"}` {
		t.Fatalf("tool input: %q", completed.ToolInput)
	}
	if completed.InputPreview != "foo.go" {
		t.Fatalf("read preview = %q, want foo.go", completed.InputPreview)
	}
	if errored.InputPreview != "nope" {
		t.Fatalf("bash preview = %q, want nope", errored.InputPreview)
	}
	if !errored.ResultIsError {
		t.Fatalf("errored tool not flagged: %+v", errored)
	}
	if errored.Result != "x" {
		t.Fatalf("errored tool result = %q, want the error message %q", errored.Result, "x")
	}
}
