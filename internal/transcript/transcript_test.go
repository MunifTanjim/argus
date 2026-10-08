package transcript

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEntryMarshalStripsHeavyBodies(t *testing.T) {
	e := Entry{ID: "1", Kind: EntryTool, ToolName: "Bash", ToolID: "t1",
		ToolInput: `{"command":"ls"}`, InputPreview: "ls", Result: "a\nb", ResultIsError: true}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "toolInput") || strings.Contains(s, `"result"`) {
		t.Errorf("heavy bodies on the wire: %s", s)
	}
	for _, want := range []string{`"kind":"tool"`, `"toolName":"Bash"`, `"inputPreview":"ls"`, `"resultIsError":true`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if e.ToolInput == "" || e.Result == "" {
		t.Error("MarshalJSON mutated the receiver")
	}
}

func TestTurnEndMarshal(t *testing.T) {
	e := Entry{ID: "3.end", Kind: EntryTurnEnd, ModelName: "Opus 4.8",
		Usage: Usage{Output: 30}, DurationMs: 1200, Interrupted: true, HasContext: true, ContextPct: 45}
	b, _ := json.Marshal(e)
	s := string(b)
	for _, want := range []string{`"kind":"turn_end"`, `"modelName":"Opus 4.8"`, `"usage":{"output":30}`,
		`"durationMs":1200`, `"interrupted":true`, `"contextPct":45`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
}

func TestEntryIsTeammate(t *testing.T) {
	tm := Entry{Kind: EntrySubagent, Subagents: []Subagent{{Name: "a", IsTeammate: true}}}
	sp := Entry{Kind: EntrySubagent, Subagents: []Subagent{{Type: "Explore"}}}
	if !tm.IsTeammate() || sp.IsTeammate() {
		t.Errorf("IsTeammate: teammate=%v spawn=%v", tm.IsTeammate(), sp.IsTeammate())
	}
}

func TestEntryIsToolCall(t *testing.T) {
	cases := []struct {
		e    Entry
		want bool
	}{
		{Entry{Kind: EntryTool}, true},
		{Entry{Kind: EntrySkill}, true},
		{Entry{Kind: EntrySubagent, Subagents: []Subagent{{Type: "Explore"}}}, true},
		{Entry{Kind: EntrySubagent, Subagents: []Subagent{{IsTeammate: true}}}, false},
		{Entry{Kind: EntryText}, false},
	}
	for _, c := range cases {
		if got := c.e.IsToolCall(); got != c.want {
			t.Errorf("IsToolCall(%+v) = %v, want %v", c.e, got, c.want)
		}
	}
}

func TestTranscriptViewJSON(t *testing.T) {
	b, _ := json.Marshal(TranscriptView{Entries: []Entry{{ID: "0", Kind: EntryUser, Text: "hi"}}})
	if !strings.HasPrefix(string(b), `{"entries":[`) {
		t.Errorf("view json = %s", b)
	}
}
