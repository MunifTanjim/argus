package codex

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

func strp(s string) *string { return &s }

func TestThreadTrackableAndTitle(t *testing.T) {
	cases := []struct {
		name      string
		th        cxThread
		trackable bool
		title     string
	}{
		{"named", cxThread{Path: strp("/r.jsonl"), Name: strp("Fix bug"), Preview: "fix the bug\nmore"}, true, "Fix bug"},
		{"preview first line", cxThread{Path: strp("/r.jsonl"), Preview: "fix the bug\nmore"}, true, "fix the bug"},
		{"title generator", cxThread{Ephemeral: true}, false, ""},
		{"no rollout path", cxThread{Path: nil}, false, ""},
		{"empty rollout path", cxThread{Path: strp("")}, false, ""},
	}
	for _, c := range cases {
		if got := c.th.trackable(); got != c.trackable {
			t.Errorf("%s: trackable = %v; want %v", c.name, got, c.trackable)
		}
		if c.trackable {
			if got := c.th.title(); got != c.title {
				t.Errorf("%s: title = %q; want %q", c.name, got, c.title)
			}
		}
	}
}

func TestThreadStatusWaiting(t *testing.T) {
	if (threadStatus{Type: "active"}).waiting() {
		t.Error("active without flags is not waiting")
	}
	if !(threadStatus{Type: "active", ActiveFlags: []string{"waitingOnApproval"}}).waiting() {
		t.Error("waitingOnApproval should be waiting")
	}
	if (threadStatus{Type: "idle"}).waiting() {
		t.Error("idle is not waiting")
	}
}

func TestParseCommandApproval(t *testing.T) {
	m := inbound{
		ID:     json.RawMessage(`0`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"t1","turnId":"u1","itemId":"i1","reason":"May I create a file?","command":"/usr/bin/zsh -lc 'touch f'","cwd":"/w","availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["touch","f"]}},"cancel"]}`),
	}
	pr, ok := parseRequest(m)
	if !ok {
		t.Fatal("command approval should parse")
	}
	if pr.threadID != "t1" || string(pr.id) != "0" {
		t.Fatalf("pr = %+v", pr)
	}
	ix := pr.interaction
	if ix.Kind != session.InteractionPermission || ix.ToolName != "exec_command" || ix.Message != "May I create a file?" {
		t.Fatalf("interaction = %+v", ix)
	}
	var in struct{ Cmd, Workdir string }
	_ = json.Unmarshal([]byte(ix.ToolInput), &in)
	if in.Cmd != "/usr/bin/zsh -lc 'touch f'" || in.Workdir != "/w" {
		t.Fatalf("tool input = %s", ix.ToolInput)
	}
	// acceptForSession is not offered by this request, so only Allow/Deny.
	if len(ix.Options) != 2 || ix.Options[0].Value != "allow" || ix.Options[1].Value != "deny" || !ix.Options[1].Reject {
		t.Fatalf("options = %+v", ix.Options)
	}
}

func TestParseCommandApprovalOffersSession(t *testing.T) {
	m := inbound{ID: json.RawMessage(`1`), Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"threadId":"t1","command":"ls","cwd":"/w","availableDecisions":["accept","acceptForSession","decline"]}`)}
	pr, _ := parseRequest(m)
	vals := []string{}
	for _, o := range pr.interaction.Options {
		vals = append(vals, o.Value)
	}
	if !reflect.DeepEqual(vals, []string{"allow", "acceptForSession", "deny"}) {
		t.Fatalf("option values = %v", vals)
	}
}

func TestParseFileChangeAndPermissions(t *testing.T) {
	fc, ok := parseRequest(inbound{ID: json.RawMessage(`2`), Method: "item/fileChange/requestApproval",
		Params: json.RawMessage(`{"threadId":"t1","reason":"edit files"}`)})
	if !ok || fc.interaction.ToolName != "apply_patch" || fc.interaction.Message != "edit files" || len(fc.interaction.Options) != 3 {
		t.Fatalf("file change = %+v ok=%v", fc, ok)
	}
	pm, ok := parseRequest(inbound{ID: json.RawMessage(`3`), Method: "item/permissions/requestApproval",
		Params: json.RawMessage(`{"threadId":"t1","reason":"need net","permissions":{"network":{"enabled":true}}}`)})
	if !ok || pm.interaction.ToolName != "permissions" || pm.interaction.ToolInput != `{"network":{"enabled":true}}` {
		t.Fatalf("permissions = %+v ok=%v", pm, ok)
	}
}

func TestParseUserInput(t *testing.T) {
	pr, ok := parseRequest(inbound{ID: json.RawMessage(`4`), Method: "item/tool/requestUserInput",
		Params: json.RawMessage(`{"threadId":"t1","isBlocking":true,"questions":[{"id":"q1","header":"DB","question":"Which db?","options":[{"label":"pg","description":"Postgres"},{"label":"sqlite","description":"SQLite"}]}]}`)})
	if !ok || pr.interaction.Kind != session.InteractionQuestion || len(pr.interaction.Questions) != 1 {
		t.Fatalf("user input = %+v ok=%v", pr, ok)
	}
	q := pr.interaction.Questions[0]
	if q.Header != "DB" || q.Question != "Which db?" || !reflect.DeepEqual(q.Options, []string{"pg", "sqlite"}) ||
		!reflect.DeepEqual(q.OptionDescriptions, []string{"Postgres", "SQLite"}) {
		t.Fatalf("question = %+v", q)
	}
}

func TestParseUnknownRequestIgnored(t *testing.T) {
	if _, ok := parseRequest(inbound{ID: json.RawMessage(`5`), Method: "currentTime/read", Params: json.RawMessage(`{"threadId":"t1"}`)}); ok {
		t.Fatal("unsupported requests must be left for the TUI")
	}
}

func TestReplyFor(t *testing.T) {
	cmd := &pendingRequest{method: "item/commandExecution/requestApproval"}
	if got := replyFor(cmd, api.RespondParams{OptionValue: "allow"}); !reflect.DeepEqual(got, map[string]any{"decision": "accept"}) {
		t.Errorf("allow = %v", got)
	}
	if got := replyFor(cmd, api.RespondParams{OptionValue: "acceptForSession"}); !reflect.DeepEqual(got, map[string]any{"decision": "acceptForSession"}) {
		t.Errorf("session = %v", got)
	}
	if got := replyFor(cmd, api.RespondParams{Behavior: "deny", Reason: "no"}); !reflect.DeepEqual(got, map[string]any{"decision": "decline"}) {
		t.Errorf("deny = %v", got)
	}

	perm := &pendingRequest{method: "item/permissions/requestApproval", permissions: json.RawMessage(`{"network":{"enabled":true}}`)}
	b, _ := json.Marshal(replyFor(perm, api.RespondParams{OptionValue: "allow"}))
	if string(b) != `{"permissions":{"network":{"enabled":true}},"scope":"turn"}` {
		t.Errorf("perm allow = %s", b)
	}
	b, _ = json.Marshal(replyFor(perm, api.RespondParams{OptionValue: "deny"}))
	if string(b) != `{"permissions":{},"scope":"turn"}` {
		t.Errorf("perm deny = %s", b)
	}

	ui := &pendingRequest{method: "item/tool/requestUserInput", questions: []userInputQuestion{{ID: "q1", Question: "Which db?"}, {ID: "q2", Question: "Pick many"}}}
	b, _ = json.Marshal(replyFor(ui, api.RespondParams{Answers: map[string]any{"Which db?": "pg", "Pick many": []any{"a", "b"}}}))
	if string(b) != `{"answers":{"q1":{"answers":["pg"]},"q2":{"answers":["a","b"]}}}` {
		t.Errorf("user input = %s", b)
	}
	b, _ = json.Marshal(replyFor(ui, api.RespondParams{Answers: map[string]any{"Which db?": "pg"}}))
	if string(b) != `{"answers":{"q1":{"answers":["pg"]},"q2":{"answers":[]}}}` {
		t.Errorf("unanswered question = %s", b)
	}
	b, _ = json.Marshal(replyFor(ui, api.RespondParams{}))
	if string(b) != `{"answers":{"q1":{"answers":[]},"q2":{"answers":[]}}}` {
		t.Errorf("nothing answered = %s", b)
	}
}

func TestParseUserInputAllowsNotesAndOther(t *testing.T) {
	pr, ok := parseRequest(inbound{ID: json.RawMessage(`9`), Method: "item/tool/requestUserInput",
		Params: json.RawMessage(`{"threadId":"t1","isBlocking":true,"questions":[
			{"id":"size","header":"Size","question":"Pick a size","isOther":true,"options":[{"label":"Small","description":"S"},{"label":"Large","description":"L"}]},
			{"id":"color","header":"Color","question":"Pick a color","isOther":false,"options":[{"label":"Red","description":"R"}]}]}`)})
	if !ok || len(pr.interaction.Questions) != 2 {
		t.Fatalf("parse = %+v ok=%v", pr, ok)
	}
	size, color := pr.interaction.Questions[0], pr.interaction.Questions[1]
	if !size.AllowNotes || !color.AllowNotes {
		t.Errorf("codex questions must allow notes: %+v %+v", size, color)
	}
	if !reflect.DeepEqual(size.Options, []string{"Small", "Large", "None of the above"}) {
		t.Errorf("isOther must append None of the above: %v", size.Options)
	}
	if size.OptionDescriptions[2] != "Optionally, add details in notes" {
		t.Errorf("None of the above description = %q", size.OptionDescriptions[2])
	}
	if !reflect.DeepEqual(color.Options, []string{"Red"}) {
		t.Errorf("isOther=false must not add an option: %v", color.Options)
	}
}

func TestReplyForUserInputNotes(t *testing.T) {
	ui := &pendingRequest{method: "item/tool/requestUserInput", questions: []userInputQuestion{
		{ID: "size", Question: "Pick a size"},
		{ID: "color", Question: "Pick a color"},
		{ID: "shape", Question: "Pick a shape"},
		{ID: "mood", Question: "Pick a mood"},
	}}
	b, _ := json.Marshal(replyFor(ui, api.RespondParams{
		Answers: map[string]any{"Pick a size": "Large", "Pick a color": "None of the above", "Pick a mood": "Calm"},
		Notes:   map[string]string{"Pick a size": "  but extra large please ", "Pick a color": "green actually", "Pick a shape": "round", "Pick a mood": "   "},
	}))
	want := `{"answers":{"color":{"answers":["None of the above","user_note: green actually"]},` +
		`"mood":{"answers":["Calm"]},` +
		`"shape":{"answers":["user_note: round"]},` +
		`"size":{"answers":["Large","user_note: but extra large please"]}}}`
	if string(b) != want {
		t.Errorf("reply =\n%s\nwant\n%s", b, want)
	}
}

// A free-form question (no options, no isOther) keeps argus's type-your-own
// row: with notes it would have nothing to select.
func TestParseUserInputWithoutOptionsKeepsFreeText(t *testing.T) {
	pr, ok := parseRequest(inbound{ID: json.RawMessage(`10`), Method: "item/tool/requestUserInput",
		Params: json.RawMessage(`{"threadId":"t1","questions":[{"id":"why","header":"Why","question":"Why?","isOther":false,"options":null}]}`)})
	if !ok {
		t.Fatal("parse failed")
	}
	if q := pr.interaction.Questions[0]; q.AllowNotes || len(q.Options) != 0 {
		t.Errorf("option-less question = %+v, want AllowNotes false and no options", q)
	}
}
