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
	if !errored.ResultIsError {
		t.Fatalf("errored tool not flagged: %+v", errored)
	}
}
