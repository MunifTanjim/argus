package opencode

import (
	"encoding/json"
	"reflect"
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
	items := foldMessages(env.Data, false)

	var sub, failed transcript.Entry
	for _, it := range items {
		switch it.ToolName {
		case "subagent":
			sub = it
		case "task":
			failed = it
		}
	}

	// A subagent with a child session id becomes a drillable ItemSubagent.
	if sub.Kind != transcript.EntrySubagent {
		t.Fatalf("subagent kind = %q, want %q", sub.Kind, transcript.EntrySubagent)
	}
	if len(sub.Subagents) != 1 {
		t.Fatalf("want 1 subagent ref, got %d", len(sub.Subagents))
	}
	s := sub.Subagents[0]
	if s.ID != "ses_child" || s.Type != "explore" || s.Desc != "look at X" || !s.HasTrace {
		t.Fatalf("subagent ref = %+v", s)
	}

	// A task that never spawned a child (no metadata.sessionID) stays a plain tool.
	if failed.Kind != transcript.EntryTool {
		t.Fatalf("childless task kind = %q, want %q", failed.Kind, transcript.EntryTool)
	}
}

func TestFoldMessages(t *testing.T) {
	var env ocEnvelope[ocMessage]
	if err := json.Unmarshal([]byte(messagesFixture), &env); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	es := foldMessages(env.Data, false)
	// user, thinking, text, tool, tool; no footer since no user message follows.
	if len(es) != 5 {
		t.Fatalf("want 5 entries, got %d: %+v", len(es), es)
	}
	if es[0].Kind != transcript.EntryUser || es[0].Text != "hello" {
		t.Fatalf("user entry: %+v", es[0])
	}
	var kinds []transcript.EntryKind
	for _, e := range es[1:] {
		kinds = append(kinds, e.Kind)
	}
	if len(kinds) != 4 || kinds[0] != transcript.EntryThinking || kinds[1] != transcript.EntryText || kinds[2] != transcript.EntryTool || kinds[3] != transcript.EntryTool {
		t.Fatalf("entry kinds: %v", kinds)
	}
	// A following user message closes the run with a footer.
	closed := foldMessages(append(env.Data, ocUser("m9", "next")), false)
	end := closed[len(closed)-2]
	if end.Kind != transcript.EntryTurnEnd || end.ModelName != "glm" || end.Thinking != 1 || end.ToolCount != 2 {
		t.Fatalf("footer: %+v", end)
	}
	var completed, errored transcript.Entry
	for _, e := range es {
		if e.ToolName == "read" {
			completed = e
		}
		if e.ToolName == "bash" {
			errored = e
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

func ocUser(id, text string) ocMessage {
	return ocMessage{Type: "user", ID: id, Text: text}
}

func ocAssistant(id, model string, parts ...ocPart) ocMessage {
	return ocMessage{Type: "assistant", ID: id, Model: ocModelRef{ID: model}, Content: parts}
}

func TestFoldMessagesOneFooterPerAssistantRun(t *testing.T) {
	es := foldMessages([]ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "gpt-5", ocPart{Type: "reasoning", ID: "p1", Text: "hmm"}),
		ocAssistant("m3", "gpt-5", ocPart{Type: "text", ID: "p2", Text: "hello"}),
		ocUser("m4", "next"),
	}, false)
	var got []transcript.EntryKind
	for _, e := range es {
		got = append(got, e.Kind)
	}
	want := []transcript.EntryKind{transcript.EntryUser, transcript.EntryThinking, transcript.EntryText,
		transcript.EntryTurnEnd, transcript.EntryUser}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	end := es[3]
	if end.ID != "m3.end" || end.ModelName != "gpt-5" || end.Thinking != 1 {
		t.Errorf("footer = %+v", end)
	}
}

func TestFoldMessagesTrailingRunHasNoFooter(t *testing.T) {
	es := foldMessages([]ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "gpt-5", ocPart{Type: "text", ID: "p1", Text: "hello"}),
	}, false)
	if es[len(es)-1].Kind == transcript.EntryTurnEnd {
		t.Fatalf("trailing run got a footer: %+v", es)
	}
}

func TestFoldMessagesFinishedClosesTrailingRun(t *testing.T) {
	es := foldMessages([]ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "gpt-5", ocPart{Type: "reasoning", ID: "p0", Text: "hmm"}, ocPart{Type: "text", ID: "p1", Text: "hello"}),
	}, true)
	end := es[len(es)-1]
	if end.Kind != transcript.EntryTurnEnd || end.ID != "m2.end" || end.ModelName != "gpt-5" || end.Thinking != 1 {
		t.Fatalf("finished trailing run footer = %+v", end)
	}
	// A finished session whose last turn produced nothing gets no footer.
	es = foldMessages([]ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "gpt-5", ocPart{Type: "text", ID: "p1", Text: "  "}),
	}, true)
	if es[len(es)-1].Kind == transcript.EntryTurnEnd {
		t.Fatalf("empty finished turn got a footer: %+v", es)
	}
}

func TestFoldMessagesStableIDs(t *testing.T) {
	short := []ocMessage{ocUser("m1", "hi"), ocAssistant("m2", "x", ocPart{Type: "text", ID: "p1", Text: "a"})}
	long := append(append([]ocMessage{}, short...), ocUser("m3", "more"))
	a, b := foldMessages(short, false), foldMessages(long, false)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("entry %d id %q vs %q", i, a[i].ID, b[i].ID)
		}
	}
}

func TestFoldMessagesEmptyTurnHasNoFooter(t *testing.T) {
	es := foldMessages([]ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "gpt-5", ocPart{Type: "text", ID: "p1", Text: "  "}),
		ocUser("m3", "next"),
	}, false)
	for _, e := range es {
		if e.Kind == transcript.EntryTurnEnd {
			t.Fatalf("empty turn got a footer: %+v", es)
		}
	}
}

func TestFoldMessagesFooterTimestamp(t *testing.T) {
	a := ocAssistant("m2", "x", ocPart{Type: "text", ID: "p1", Text: "a"})
	a.Time.Created = 5000
	es := foldMessages([]ocMessage{ocUser("m1", "hi"), a, ocUser("m3", "n")}, false)
	if got := es[2].Timestamp; got != tsMillis(5000) {
		t.Fatalf("footer timestamp = %q", got)
	}
}

// OpenCode sends "id": null for reasoning and text parts; their entries still
// need distinct ids that survive a re-fold.
func TestFoldMessagesIDlessPartsGetDistinctStableIDs(t *testing.T) {
	msgs := []ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "x",
			ocPart{Type: "reasoning", Text: "hmm"},
			ocPart{Type: "text", Text: "hello"},
			ocPart{Type: "tool", ID: "functions.read:0", Name: "read"}),
		ocAssistant("m3", "x", ocPart{Type: "text", Text: "done"}),
	}
	es := foldMessages(msgs, false)
	seen := map[string]bool{}
	for _, e := range es {
		if e.ID == "" || seen[e.ID] {
			t.Fatalf("entry ids not distinct/non-empty: %+v", es)
		}
		seen[e.ID] = true
	}
	more := append(append([]ocMessage{}, msgs...), ocUser("m4", "again"))
	again := foldMessages(more, false)
	for i := range es {
		if again[i].ID != es[i].ID {
			t.Fatalf("entry %d id %q changed to %q on re-fold", i, es[i].ID, again[i].ID)
		}
	}
	if es[3].ID != "functions.read:0" {
		t.Errorf("tool entry id = %q, want the part id", es[3].ID)
	}
}

func TestFoldMessagesIdleClosesLiveTurn(t *testing.T) {
	idle := ocMessage{Type: "idle", ID: "m3"}
	idle.Time.Created = 1791306227401
	msgs := []ocMessage{
		ocUser("m1", "hi"),
		ocAssistant("m2", "gpt-5", ocPart{Type: "text", ID: "p1", Text: "hello"}),
		idle,
	}
	es := foldMessages(msgs, false)
	if len(es) != 3 || es[2].Kind != transcript.EntryTurnEnd {
		t.Fatalf("want user, text, footer; got %+v", es)
	}
	if es[2].ID != "m2.end" || es[2].Timestamp != tsMillis(idle.Time.Created) {
		t.Errorf("footer = %+v, want id m2.end stamped at the idle time", es[2])
	}
	// A following prompt must not emit the footer a second time.
	es = foldMessages(append(msgs, ocUser("m4", "next")), false)
	n := 0
	for _, e := range es {
		if e.Kind == transcript.EntryTurnEnd {
			n++
		}
	}
	if n != 1 {
		t.Errorf("footers = %d, want 1", n)
	}
}

func TestFoldMessagesIdleAfterEmptyTurnAddsNothing(t *testing.T) {
	es := foldMessages([]ocMessage{ocUser("m1", "hi"), ocAssistant("m2", "x"), {Type: "idle", ID: "m3"}}, false)
	if len(es) != 1 {
		t.Fatalf("want only the user entry, got %+v", es)
	}
}

func TestFoldMessagesProviderErrorIsSurfaced(t *testing.T) {
	failed := ocAssistant("m2", "glm-5.3")
	failed.Finish = "error"
	failed.Error = &ocMessageError{Type: "provider.quota", Message: "Go usage limit exceeded", Status: 429}
	failed.Time.Created = 1791065684003
	es := foldMessages([]ocMessage{ocUser("m1", "hi"), failed, {Type: "idle", ID: "m3"}}, false)
	if len(es) != 2 {
		t.Fatalf("want user and error entries, got %+v", es)
	}
	e := es[1]
	if e.Kind != transcript.EntrySystem || !e.IsError || e.ID != "m2.error" {
		t.Fatalf("error entry = %+v", e)
	}
	if e.Label != "Go usage limit exceeded" || e.Timestamp != tsMillis(failed.Time.Created) {
		t.Errorf("error entry = %+v, want the message as its label", e)
	}
	if e.Detail != "provider.quota (429)\nGo usage limit exceeded" {
		t.Errorf("detail = %q", e.Detail)
	}
}
