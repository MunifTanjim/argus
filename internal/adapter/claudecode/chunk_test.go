package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSession writes a parent transcript plus a linked subagent file laid out
// the way Claude Code does, and returns the parent path.
func writeSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sessionID := "11111111-1111-1111-1111-111111111111"
	parentPath := filepath.Join(dir, sessionID+".jsonl")

	parent := `{"type":"user","uuid":"u1","timestamp":"2026-06-12T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"map the code"}]}}
{"type":"assistant","uuid":"a1","timestamp":"2026-06-12T10:00:01Z","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"tool_use","usage":{"input_tokens":1000,"output_tokens":20,"cache_read_input_tokens":500},"content":[{"type":"thinking","thinking":"let me explore"},{"type":"text","text":"on it"},{"type":"tool_use","id":"T1","name":"Task","input":{"subagent_type":"Explore","description":"map code"}}]}}
{"type":"user","uuid":"u2","isSidechain":false,"toolUseResult":{"agentId":"abc123","status":"completed"},"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"T1","content":"explored"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-06-12T10:00:09Z","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"end_turn","usage":{"input_tokens":1200,"output_tokens":40,"cache_read_input_tokens":900},"content":[{"type":"text","text":"all done"}]}}
`
	if err := os.WriteFile(parentPath, []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(dir, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := `{"type":"user","uuid":"s1","isSidechain":true,"agentId":"abc123","message":{"role":"user","content":[{"type":"text","text":"map code"}]}}
{"type":"assistant","uuid":"s2","isSidechain":true,"agentId":"abc123","message":{"role":"assistant","model":"claude-opus-4-8","content":[{"type":"text","text":"mapped it"}]}}
`
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc123.jsonl"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	return parentPath
}

func TestReadTranscriptViewShellChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "22222222-2222-2222-2222-222222222222.jsonl")
	session := `{"type":"user","uuid":"u1","timestamp":"2026-06-12T10:00:00Z","message":{"role":"user","content":"<bash-input>echo hi</bash-input>"}}
{"type":"user","uuid":"u2","timestamp":"2026-06-12T10:00:01Z","message":{"role":"user","content":"<bash-stdout>hi</bash-stdout>"}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	var shell *Chunk
	for i := range view.Chunks {
		if view.Chunks[i].Kind == ChunkShell {
			shell = &view.Chunks[i]
		}
	}
	if shell == nil {
		t.Fatalf("no shell chunk found in %+v", view.Chunks)
	}
	if shell.Text != "echo hi" {
		t.Errorf("Text = %q, want the command %q", shell.Text, "echo hi")
	}
	if shell.Detail != "hi" {
		t.Errorf("Detail = %q, want the output %q", shell.Detail, "hi")
	}
}

func TestReadTranscriptViewSkillChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "33333333-3333-3333-3333-333333333333.jsonl")
	session := `{"type":"user","uuid":"u1","timestamp":"2026-07-04T10:00:00Z","message":{"role":"user","content":"<command-message>superpowers:brainstorming</command-message>\n<command-name>/superpowers:brainstorming</command-name>"}}
{"type":"user","isMeta":true,"uuid":"u2","timestamp":"2026-07-04T10:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /Users/x/.claude/skills/brainstorming\n\n# Brainstorming Ideas Into Designs\n\nHelp turn ideas into designs."}]}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	var user, skill *Chunk
	for i := range view.Chunks {
		switch view.Chunks[i].Kind {
		case ChunkUser:
			user = &view.Chunks[i]
		case ChunkSkill:
			skill = &view.Chunks[i]
		}
	}
	if user == nil || user.Text != "/superpowers:brainstorming" {
		t.Fatalf("user's slash command should stay its own chunk, got %+v", view.Chunks)
	}
	if skill == nil {
		t.Fatalf("no skill chunk found in %+v", view.Chunks)
	}
	if skill.Text != "superpowers:brainstorming" {
		t.Errorf("Text = %q, want the skill identifier", skill.Text)
	}
	if skill.Label != "/Users/x/.claude/skills/brainstorming" {
		t.Errorf("Label = %q, want the source path", skill.Label)
	}
	if !strings.HasPrefix(skill.Detail, "# Brainstorming Ideas Into Designs") {
		t.Errorf("Detail = %q, want the skill body", skill.Detail)
	}
}

func TestReadTranscriptViewGrouping(t *testing.T) {
	view, err := ReadTranscriptView(writeSession(t))
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}

	var user, ai *Chunk
	for i := range view.Chunks {
		switch view.Chunks[i].Kind {
		case ChunkUser:
			if user == nil {
				user = &view.Chunks[i]
			}
		case ChunkAI:
			if ai == nil {
				ai = &view.Chunks[i]
			}
		}
	}
	if user == nil || user.Text != "map the code" {
		t.Fatalf("user chunk = %+v", user)
	}
	if ai == nil {
		t.Fatalf("no AI chunk in %+v", view.Chunks)
	}
	// a1 + a2 merge into one AI chunk (the tool-result-only turn doesn't split it).
	if ai.Model != "claude-opus-4-8" {
		t.Errorf("model = %q", ai.Model)
	}
	if ai.Thinking != 1 || ai.ToolCount != 1 {
		t.Errorf("stats: thinking=%d toolCount=%d", ai.Thinking, ai.ToolCount)
	}
	if ai.Usage.Output == 0 || ai.Usage.Context() == 0 {
		t.Errorf("usage not mapped: %+v", ai.Usage)
	}
	if !ai.HasContext || ai.ContextPct <= 0 {
		t.Errorf("context delta not populated: has=%v pct=%v", ai.HasContext, ai.ContextPct)
	}
	if lo, ok := ai.LastOutput(); !ok || lo.Kind != ItemText || lo.Text != "all done" {
		t.Errorf("last output = %+v (ok=%v)", lo, ok)
	}
}

func TestReadTranscriptViewSubagentTrace(t *testing.T) {
	session := writeSession(t)
	view, err := ReadTranscriptView(session)
	if err != nil {
		t.Fatalf("ReadTranscriptView: %v", err)
	}
	var sub *Item
	for ci := range view.Chunks {
		for ii := range view.Chunks[ci].Items {
			if view.Chunks[ci].Items[ii].Kind == ItemSubagent {
				sub = &view.Chunks[ci].Items[ii]
			}
		}
	}
	if sub == nil {
		t.Fatalf("no subagent item found in %+v", view.Chunks)
	}
	if len(sub.Subagents) != 1 {
		t.Fatalf("want 1 subagent, got %d", len(sub.Subagents))
	}
	sa := sub.Subagents[0]
	if sa.Type != "Explore" {
		t.Errorf("subagent type = %q, want Explore", sa.Type)
	}
	if sa.ID != "abc123" {
		t.Errorf("agent id = %q, want abc123", sa.ID)
	}
	// Lazy contract: the item is drillable but its trace is NOT inlined.
	if !sa.HasTrace {
		t.Errorf("subagent item should be drillable (HasTrace)")
	}
	if len(sa.Trace) != 0 {
		t.Errorf("trace should not be inlined, got %d chunks", len(sa.Trace))
	}
	// The trace is fetched on demand via ReadSubagentView.
	tv, ok, err := ReadSubagentView(session, "abc123")
	if err != nil || !ok {
		t.Fatalf("ReadSubagentView(abc123) ok=%v err=%v", ok, err)
	}
	if len(tv.Chunks) == 0 {
		t.Fatalf("fetched subagent trace is empty")
	}
	last := tv.Chunks[len(tv.Chunks)-1]
	if last.Kind != ChunkAI || last.Text == "" {
		if lo, ok := last.LastOutput(); !ok || lo.Text != "mapped it" {
			t.Errorf("unexpected trace tail: %+v (lastOutput ok=%v)", last, ok)
		}
	}
}

func TestItemMarshalStripsHeavyBodies(t *testing.T) {
	it := Item{
		ID: "i1", Kind: ItemTool, ToolName: "Read", ToolID: "T9",
		ToolInput: `{"file_path":"/big/file"}`, InputPreview: "file", Result: "lots of content", ResultIsError: true,
	}
	b, err := json.Marshal(it)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, dropped := range []string{"toolInput", "lots of content", "/big/file"} {
		if strings.Contains(s, dropped) {
			t.Errorf("expected %q stripped, got %s", dropped, s)
		}
	}
	// The light fields the timeline needs (and the addressing key) survive.
	for _, kept := range []string{`"toolId":"T9"`, `"toolName":"Read"`, `"inputPreview":"file"`, `"resultIsError":true`} {
		if !strings.Contains(s, kept) {
			t.Errorf("expected %q retained, got %s", kept, s)
		}
	}
	// The in-memory item is untouched (node-side lookups still see the bodies).
	if it.ToolInput == "" || it.Result == "" {
		t.Errorf("MarshalJSON mutated the source item: %+v", it)
	}
}

func TestFindToolDetail(t *testing.T) {
	path := writeSession(t)

	// Subagent tool in the parent transcript (the Task tool_use), addressed in the
	// session transcript (agentID empty).
	td, ok, err := FindToolDetail(path, "", "T1")
	if err != nil || !ok {
		t.Fatalf("FindToolDetail(T1) ok=%v err=%v", ok, err)
	}
	if !strings.Contains(td.ToolInput, "Explore") {
		t.Errorf("T1 input = %q, want it to carry the subagent input", td.ToolInput)
	}
	if td.Result == "" {
		t.Errorf("T1 result empty, want the tool_result content")
	}

	// Unknown tool id → not found, no error.
	if _, ok, err := FindToolDetail(path, "", "nope"); ok || err != nil {
		t.Errorf("FindToolDetail(nope) ok=%v err=%v, want false/nil", ok, err)
	}

	// Unknown subagent → not found (resolved file doesn't exist).
	if _, ok, err := FindToolDetail(path, "missing", "T1"); ok || err != nil {
		t.Errorf("FindToolDetail(agent=missing) ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestChunkMarshalStampsPreviewItemID(t *testing.T) {
	c := Chunk{
		ID:   "c1",
		Kind: ChunkAI,
		Items: []Item{
			{ID: "i1", Kind: ItemText, Text: "first"},
			{ID: "i2", Kind: ItemTool, ToolName: "Bash"},
			{ID: "i3", Kind: ItemText, Text: "final answer"},
		},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		PreviewItemID string `json:"previewItemId"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PreviewItemID != "i3" {
		t.Fatalf("previewItemId = %q, want i3", got.PreviewItemID)
	}

	// A chunk with no preview-worthy items omits the field.
	empty := Chunk{ID: "c2", Kind: ChunkUser, Text: "hi"}
	b2, _ := json.Marshal(empty)
	if strings.Contains(string(b2), "previewItemId") {
		t.Fatalf("expected previewItemId omitted, got %s", b2)
	}
}
