package memory

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestParseCaptureResultFenced(t *testing.T) {
	in := "Here you go:\n```json\n{\"ops\":[{\"action\":\"add\",\"file\":\"USER.md\",\"text\":\"Prefers Go\",\"quote\":\"I like Go\"}],\"log\":\"did a thing\"}\n```\n"
	ops, log, err := ParseCaptureResult(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0] != (Op{Action: "add", File: "USER.md", Text: "Prefers Go"}) || log != "did a thing" {
		t.Fatalf("ops = %+v, log = %q", ops, log)
	}
	res, err := ParseCapture(in)
	if err != nil || res.Ops[0].Quote != "I like Go" {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestParseCaptureResultPlainAndInvalid(t *testing.T) {
	ops, log, err := ParseCaptureResult(`{"ops":[],"log":""}`)
	if err != nil || len(ops) != 0 || log != "" {
		t.Fatalf("%v %q %v", ops, log, err)
	}
	if _, _, err := ParseCaptureResult("nothing useful"); err == nil {
		t.Fatal("want error")
	}
}

func TestFilterQuoted(t *testing.T) {
	users := []string{"Hello  there,\nI really like   Go a lot"}
	ops := []CaptureOp{
		{Op: Op{Action: "add", File: "USER.md", Text: "a"}, Quote: "i really like go"},
		{Op: Op{Action: "add", File: "USER.md", Text: "b"}},
		{Op: Op{Action: "add", File: "memory/USER.md", Text: "c"}},
		{Op: Op{Action: "add", File: "MEMORY.md", Text: "d"}},
		{Op: Op{Action: "add", File: "MEMORY.md", Text: "e"}, Quote: "never said"},
	}
	got := FilterQuoted(ops, users)
	var texts []string
	for _, o := range got {
		texts = append(texts, o.Text)
	}
	if strings.Join(texts, ",") != "a,d" {
		t.Fatalf("kept %v", texts)
	}
}

func TestBuildCapturePrompt(t *testing.T) {
	p := BuildCapturePrompt("- fact one", "- likes tea", []string{"cooking"}, []transcript.Entry{
		{Kind: transcript.EntryUser, Text: "hi"}, {Kind: transcript.EntryText, Text: "hello"},
	})
	for _, want := range []string{"fact one", "likes tea", "cooking", "User: hi", "Agent: hello", `"quote"`, `"ops"`} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
}
