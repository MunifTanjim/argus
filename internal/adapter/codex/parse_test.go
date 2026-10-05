package codex

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestExecExitCode(t *testing.T) {
	cases := []struct {
		name   string
		output string
		code   int
		ok     bool
	}{
		{"zero", "Chunk ID: x\nWall time: 0.01 seconds\nProcess exited with code 0\nOriginal token count: 1\nOutput:\nhi\n", 0, true},
		{"nonzero", "Chunk ID: x\nWall time: 0.01 seconds\nProcess exited with code 127\nOriginal token count: 1\nOutput:\ncommand not found\n", 127, true},
		{"no match", "Plan updated", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, ok := execExitCode(tc.output)
			if ok != tc.ok || code != tc.code {
				t.Errorf("execExitCode(%q) = (%d, %v), want (%d, %v)", tc.output, code, ok, tc.code, tc.ok)
			}
		})
	}
}

func TestParseRolloutExecCommandResultIsError(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	for _, it := range entries {
		if it.ToolName == "exec_command" && it.Result != "" && it.ResultIsError {
			t.Errorf("exit-0 exec_command marked as error: %+v", it)
		}
	}
}

func TestParseRolloutMessages(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-child.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	// child = 1 real user message + 1 assistant turn; environment_context filtered.
	var user, ai int
	for _, e := range entries {
		switch e.Kind {
		case transcript.EntryUser:
			user++
			if wantPrefix := "This is a reference-session subagent task"; !hasPrefix(e.Text, wantPrefix) {
				t.Fatalf("user text = %q, want prefix %q", e.Text, wantPrefix)
			}
		case transcript.EntryTurnEnd:
			ai++
		}
	}
	if user != 1 || ai != 1 {
		t.Fatalf("want user=1 ai=1, got user=%d ai=%d", user, ai)
	}
}

func TestParseRolloutFiltersScaffolding(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	for _, e := range entries {
		if e.Kind != transcript.EntryUser {
			continue
		}
		if hasPrefix(e.Text, "<environment_context>") || hasPrefix(e.Text, "<subagent_notification>") {
			t.Fatalf("scaffolding user message leaked: %q", e.Text[:40])
		}
	}
}

func TestParseRolloutTools(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	byID := map[string]transcript.Entry{}
	for _, it := range entries {
		if it.Kind == transcript.EntryTool {
			byID[it.ToolID] = it
		}
	}
	var sawExec, sawResult bool
	for _, it := range byID {
		if it.ToolName == "exec_command" {
			sawExec = true
			if it.Result != "" {
				sawResult = true
			}
		}
	}
	if !sawExec {
		t.Fatal("no exec_command tool item found")
	}
	if !sawResult {
		t.Fatal("exec_command tool item has no paired result")
	}
	found := false
	for _, it := range byID {
		if it.ToolName == "update_plan" && it.Result == "Plan updated" {
			found = true
		}
	}
	if !found {
		t.Fatal("update_plan result 'Plan updated' not paired")
	}
}

func TestParseRolloutReasoningEncryptedShown(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	thinking, turnThinking := 0, 0
	for _, it := range entries {
		if it.Kind == transcript.EntryThinking {
			thinking++
			turnThinking++
			if it.Text != "" {
				t.Errorf("encrypted reasoning should have empty text, got %q", it.Text)
			}
		}
		if it.Kind == transcript.EntryTurnEnd {
			if it.Thinking != turnThinking {
				t.Errorf("Thinking count %d != thinking entries %d", it.Thinking, turnThinking)
			}
			turnThinking = 0
		}
	}
	if thinking == 0 {
		t.Fatal("expected thinking items from encrypted reasoning steps")
	}
}

func TestParseRolloutReasoningSummary(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"planning the fix"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var think string
	for _, it := range entries {
		if it.Kind == transcript.EntryThinking {
			think = it.Text
		}
	}
	if think != "planning the fix" {
		t.Fatalf("thinking text = %q, want %q", think, "planning the fix")
	}
}

func TestParseRolloutUsageAndContext(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var withCtx, withDur, withUsage int
	for _, c := range entries {
		if c.Kind != transcript.EntryTurnEnd {
			continue
		}
		if c.HasContext {
			withCtx++
			if c.ContextPct <= 0 || c.ContextPct > 100 {
				t.Fatalf("ContextPct out of range: %v", c.ContextPct)
			}
		}
		if c.DurationMs > 0 {
			withDur++
		}
		if c.Usage.Total() > 0 {
			withUsage++
		}
	}
	if withCtx == 0 || withDur == 0 || withUsage == 0 {
		t.Fatalf("want ctx/dur/usage present, got ctx=%d dur=%d usage=%d", withCtx, withDur, withUsage)
	}
}

func TestParseRolloutSubagentLink(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var sub transcript.Entry
	var found bool
	for _, it := range entries {
		if it.ToolName == "spawn_agent" {
			sub, found = it, true
		}
	}
	if !found {
		t.Fatal("no spawn_agent item found")
	}
	if len(sub.Subagents) != 1 {
		t.Fatalf("want 1 subagent, got %d", len(sub.Subagents))
	}
	sa := sub.Subagents[0]
	if sa.ID != "019f278e-50a5-7f83-91f2-c30e8ac18e19" {
		t.Fatalf("ID = %q, want child thread id", sa.ID)
	}
	if sa.Type != "default" {
		t.Fatalf("Type = %q, want default", sa.Type)
	}
	if sa.Desc == "" {
		t.Fatal("Desc empty, want spawn message")
	}
}

func TestParseRolloutManagementCallsAreSubagents(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	names := map[string]transcript.EntryKind{}
	for _, it := range entries {
		names[it.ToolName] = it.Kind
	}
	if names["wait_agent"] != transcript.EntrySubagent {
		t.Fatalf("wait_agent kind = %v, want subagent", names["wait_agent"])
	}
	if names["close_agent"] != transcript.EntrySubagent {
		t.Fatalf("close_agent kind = %v, want subagent", names["close_agent"])
	}
}

func TestParseRolloutApplyPatch(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"timestamp":"2026-07-03T14:19:04.289Z","type":"response_item","payload":{"type":"custom_tool_call","id":"ctc_0ba50992c17b4203016a47c4d7d28081918b55f065bef2b315","status":"completed","call_id":"call_VVQBbyuKj37ldfGTFaDkwS8R","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /private/tmp/codex-session-reference.WP4oxY\n@@\n+codex session reference temp file\n+created for integration event coverage\n*** End Patch\n"}}` + "\n" +
		`{"timestamp":"2026-07-03T14:20:32.109Z","type":"event_msg","payload":{"type":"patch_apply_end","call_id":"call_VVQBbyuKj37ldfGTFaDkwS8R","success":true}}` + "\n" +
		`{"timestamp":"2026-07-03T14:20:32.135Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_VVQBbyuKj37ldfGTFaDkwS8R","output":"Exit code: 0\nWall time: 0.1 seconds\nOutput:\nSuccess. Updated the following files:\nM /private/tmp/codex-session-reference.WP4oxY\n"}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var it transcript.Entry
	var found bool
	for _, i := range entries {
		if i.ToolName == "apply_patch" {
			it, found = i, true
		}
	}
	if !found {
		t.Fatal("no apply_patch tool item found")
	}
	if it.Kind != transcript.EntryTool {
		t.Fatalf("apply_patch kind = %v, want tool", it.Kind)
	}
	if !hasPrefix(it.ToolInput, "*** Begin Patch") {
		t.Fatalf("apply_patch ToolInput = %q, want patch text", it.ToolInput)
	}
	if !strings.Contains(it.Result, "Success. Updated the following files") {
		t.Fatalf("apply_patch Result = %q, want paired output", it.Result)
	}
}

func TestParseRolloutViewImage(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"timestamp":"2026-07-03T14:41:45.169Z","type":"response_item","payload":{"type":"function_call","id":"fc_0ba50992c17b4203016a47ca28377c8191a4369fd4baa81d77","name":"view_image","arguments":"{\"path\":\"/private/tmp/codex-view-image-example.png\",\"detail\":\"high\"}","call_id":"call_1pGoR5zyylKULy0aIIyVCahB"}}` + "\n" +
		`{"timestamp":"2026-07-03T14:41:45.311Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1pGoR5zyylKULy0aIIyVCahB","output":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo=","detail":"high"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var it transcript.Entry
	var found bool
	for _, i := range entries {
		if i.ToolName == "view_image" {
			it, found = i, true
		}
	}
	if !found {
		t.Fatal("no view_image tool item found (content-block output likely dropped the line)")
	}
	if it.ToolInput != `{"path":"/private/tmp/codex-view-image-example.png","detail":"high"}` {
		t.Fatalf("view_image ToolInput = %q", it.ToolInput)
	}
	if want := "[image, detail: high]"; it.Result != want {
		t.Fatalf("view_image Result = %q, want %q", it.Result, want)
	}
}

func TestParseRolloutUserShellCommand(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	text := "<user_shell_command>\n<command>\necho yo\n</command>\n<result>\nExit code: 0\nDuration: 0.0417 seconds\nOutput:\nyo\n\n</result>\n</user_shell_command>"
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-07-03T15:22:55.944Z",
		"type":      "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": text},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	c := entries[0]
	if c.Kind != transcript.EntryShell {
		t.Fatalf("kind = %v, want EntryShell", c.Kind)
	}
	if c.Text != "echo yo" {
		t.Fatalf("Text = %q, want %q", c.Text, "echo yo")
	}
	if !strings.Contains(c.Detail, "Output:\nyo") {
		t.Fatalf("Detail = %q, want it to contain the output", c.Detail)
	}
	if c.IsError {
		t.Fatal("IsError = true, want false for exit code 0")
	}
}

func TestParseRolloutUserShellCommandNonZeroExit(t *testing.T) {
	entries, err := parseRolloutFromText(t, "<user_shell_command>\n<command>\nfalse\n</command>\n<result>\nExit code: 1\nDuration: 0.01 seconds\nOutput:\n\n</result>\n</user_shell_command>")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(entries) != 1 || !entries[0].IsError {
		t.Fatalf("entries = %+v, want 1 entry with IsError=true", entries)
	}
}

func parseRolloutFromText(t *testing.T, text string) ([]transcript.Entry, error) {
	t.Helper()
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	line, err := json.Marshal(map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": text},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return parseRollout(p, false)
}

func TestParseRolloutSkillLoad(t *testing.T) {
	text := "<skill>\n<name>superpowers:brainstorming</name>\n<path>/Users/muniftanjim/.codex/plugins/cache/openai-curated/superpowers/3fdeeb49/skills/brainstorming/SKILL.md</path>\n---\nname: brainstorming\ndescription: \"You MUST use this before any creative work.\"\n---\n\n# Brainstorming Ideas Into Designs\n\nHelp turn ideas into fully formed designs and specs through natural collaborative dialogue.\n</skill>"
	entries, err := parseRolloutFromText(t, text)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	it := entries[0]
	if it.Kind != transcript.EntrySkill {
		t.Fatalf("item kind = %v, want ItemSkill", it.Kind)
	}
	if it.ToolName != "Skill" {
		t.Fatalf("ToolName = %q, want Skill", it.ToolName)
	}
	if it.ToolID == "" {
		t.Fatal("ToolID empty, want stable id from stampIDs")
	}
	if it.InputPreview != "superpowers:brainstorming" {
		t.Fatalf("InputPreview = %q, want skill name", it.InputPreview)
	}
	if !strings.Contains(it.ToolInput, `"skill":"superpowers:brainstorming"`) {
		t.Fatalf("ToolInput = %q, want JSON with skill name", it.ToolInput)
	}
	if strings.Contains(it.Result, "---") || strings.Contains(it.Result, "description:") {
		t.Fatalf("Result should have frontmatter stripped, got %q", it.Result)
	}
	if !strings.HasPrefix(it.Result, "# Brainstorming Ideas Into Designs") {
		t.Fatalf("Result = %q, want it to start with the body heading", it.Result)
	}
}

func TestParseRolloutWebSearch(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"timestamp":"2026-07-03T14:50:46.915Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"ws_0ba50992c17b4203016a47cc4297208191bb367cf8b5a7ba1e","query":"argus","action":{"type":"search","query":"argus","queries":["argus"]}}}` + "\n" +
		`{"timestamp":"2026-07-03T14:50:46.919Z","type":"response_item","payload":{"type":"web_search_call","id":"ws_0ba50992c17b4203016a47cc4297208191bb367cf8b5a7ba1e","status":"completed","action":{"type":"search","query":"argus","queries":["argus"]}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var it transcript.Entry
	var found int
	for _, i := range entries {
		if i.ToolName == "web_search" {
			it, found = i, found+1
		}
	}
	if found != 1 {
		t.Fatalf("web_search tool items = %d, want 1 (web_search_end duplicate should be ignored)", found)
	}
	if it.Kind != transcript.EntryTool {
		t.Fatalf("web_search kind = %v, want tool", it.Kind)
	}
	if !strings.Contains(it.ToolInput, `"query":"argus"`) {
		t.Fatalf("web_search ToolInput = %q, want query", it.ToolInput)
	}
}

func TestParseRolloutWaitCloseResolveNickname(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	const childID = "019f278e-50a5-7f83-91f2-c30e8ac18e19"
	var wait, closeIt transcript.Entry
	for _, it := range entries {
		switch it.ToolName {
		case "wait_agent":
			wait = it
		case "close_agent":
			closeIt = it
		}
	}
	wantSubs := []transcript.Subagent{{ID: childID, Name: "Volta"}}
	if !reflect.DeepEqual(wait.Subagents, wantSubs) {
		t.Fatalf("wait_agent Subagents = %+v, want %+v", wait.Subagents, wantSubs)
	}
	if !reflect.DeepEqual(closeIt.Subagents, wantSubs) {
		t.Fatalf("close_agent Subagents = %+v, want %+v", closeIt.Subagents, wantSubs)
	}
}

func TestParseRolloutAccumulatesOutputTokens(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":100000,"last_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":10},"total_token_usage":{"input_tokens":50}}}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":100000,"last_token_usage":{"input_tokens":60,"cached_input_tokens":0,"output_tokens":20},"total_token_usage":{"input_tokens":60}}}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var got int
	for _, e := range entries {
		if e.Kind == transcript.EntryTurnEnd {
			got = e.Usage.Output
		}
	}
	if got != 30 {
		t.Fatalf("Usage.Output = %d, want 30 (accumulated)", got)
	}
}

func TestParseRolloutNoFooterForUsageOnlyTurn(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":100000,"last_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":5},"total_token_usage":{"input_tokens":50}}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	for _, e := range entries {
		if e.Kind == transcript.EntryTurnEnd {
			t.Fatalf("unexpected EntryTurnEnd for a usage-only turn")
		}
	}
}

func TestParseRolloutSpawnNickname(t *testing.T) {
	entries, err := parseRollout("testdata/rollout-parent.jsonl", false)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var sub transcript.Entry
	var found bool
	for _, it := range entries {
		if it.ToolName == "spawn_agent" {
			sub, found = it, true
		}
	}
	if !found {
		t.Fatal("no spawn_agent item found")
	}
	if len(sub.Subagents) == 0 || sub.Subagents[0].Name != "Volta" {
		t.Fatalf("Subagents = %+v, want one named Volta", sub.Subagents)
	}
	if sub.Subagents[0].ID != "019f278e-50a5-7f83-91f2-c30e8ac18e19" {
		t.Fatalf("ID = %q, want child thread id (nickname change must not break linking)", sub.Subagents[0].ID)
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func skillUserLine(name, path, body string) rolloutLine {
	text := "<skill>\n<name>" + name + "</name>\n<path>" + path + "</path>\n" + body + "\n</skill>"
	return rolloutLine{
		Timestamp: "2026-07-12T00:00:00.000Z",
		Type:      "response_item",
		Payload: rolloutPayload{
			Type:    "message",
			Role:    "user",
			Content: []rolloutContent{{Type: "input_text", Text: text}},
		},
	}
}

func userMessageLine(text string) rolloutLine {
	return rolloutLine{
		Timestamp: "2026-07-12T00:00:00.000Z",
		Type:      "response_item",
		Payload: rolloutPayload{
			Type:    "message",
			Role:    "user",
			Content: []rolloutContent{{Type: "input_text", Text: text}},
		},
	}
}

func assistantLine(text string) rolloutLine {
	return rolloutLine{Timestamp: "2026-07-12T00:00:01.000Z", Type: "response_item",
		Payload: rolloutPayload{Type: "message", Role: "assistant",
			Content: []rolloutContent{{Type: "output_text", Text: text}}}}
}

func taskCompleteLine(ms int64) rolloutLine {
	return rolloutLine{Timestamp: "2026-07-12T00:01:00.000Z", Type: "event_msg",
		Payload: rolloutPayload{Type: "task_complete", DurationMs: ms}}
}

func entryKinds(es []transcript.Entry) []transcript.EntryKind {
	out := make([]transcript.EntryKind, len(es))
	for i, e := range es {
		out[i] = e.Kind
	}
	return out
}

func TestFoldRolloutFinishedClosesDeadTurn(t *testing.T) {
	// A history session that died before task_complete.
	died := []rolloutLine{userMessageLine("hi"), assistantLine("hello")}
	es := foldRollout(died, nil, true)
	last := es[len(es)-1]
	if last.Kind != transcript.EntryTurnEnd {
		t.Fatalf("finished read has no final footer: %v", entryKinds(es))
	}
	if last.Timestamp == "" {
		t.Error("footer has no timestamp; want the turn's last entry time")
	}
}

func TestFoldRolloutFooterOnTaskComplete(t *testing.T) {
	open := []rolloutLine{userMessageLine("hi"), assistantLine("hello")}
	if got := entryKinds(foldRollout(open, nil, false)); !reflect.DeepEqual(got,
		[]transcript.EntryKind{transcript.EntryUser, transcript.EntryText}) {
		t.Fatalf("open turn kinds = %v", got)
	}
	done := append(open, taskCompleteLine(4200))
	es := foldRollout(done, nil, false)
	last := es[len(es)-1]
	if last.Kind != transcript.EntryTurnEnd || last.DurationMs != 4200 {
		t.Fatalf("last = %+v, want footer with duration", last)
	}
	if last.Timestamp != "2026-07-12T00:01:00.000Z" {
		t.Errorf("footer timestamp = %q", last.Timestamp)
	}
}

func TestFoldRolloutUserBoundaryFinishesTurn(t *testing.T) {
	es := foldRollout([]rolloutLine{
		userMessageLine("one"), assistantLine("a"),
		userMessageLine("two"), assistantLine("b"),
	}, nil, false)
	want := []transcript.EntryKind{transcript.EntryUser, transcript.EntryText, transcript.EntryTurnEnd,
		transcript.EntryUser, transcript.EntryText}
	if got := entryKinds(es); !reflect.DeepEqual(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func TestFoldRolloutUserBoundaryFooterTimestamp(t *testing.T) {
	es := foldRollout([]rolloutLine{
		userMessageLine("one"), assistantLine("a"),
		userMessageLine("two"),
	}, nil, false)
	end := es[2]
	if end.Kind != transcript.EntryTurnEnd || end.Timestamp != "2026-07-12T00:00:01.000Z" {
		t.Fatalf("footer = %+v, want the turn's last entry timestamp", end)
	}
}

func TestFoldRolloutSkillFollowsUserEntry(t *testing.T) {
	es := foldRollout([]rolloutLine{
		userMessageLine("$superpowers:brainstorming Hello"),
		skillUserLine("superpowers:brainstorming", "/p", "# Body"),
	}, nil, false)
	if got := entryKinds(es); !reflect.DeepEqual(got,
		[]transcript.EntryKind{transcript.EntryUser, transcript.EntrySkill}) {
		t.Fatalf("kinds = %v", got)
	}
	sk := es[1]
	if sk.ToolName != "Skill" || sk.ToolID == "" || sk.Result == "" || sk.ToolInput == "" {
		t.Fatalf("bad skill entry: %+v", sk)
	}
}

func TestFoldRolloutStableIDs(t *testing.T) {
	short := []rolloutLine{userMessageLine("one"), assistantLine("a")}
	long := append(append([]rolloutLine{}, short...), taskCompleteLine(1), userMessageLine("two"))
	a, b := foldRollout(short, nil, false), foldRollout(long, nil, false)
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Kind != b[i].Kind {
			t.Fatalf("entry %d changed: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestFoldRolloutDropsBlankAssistantText(t *testing.T) {
	es := foldRollout([]rolloutLine{userMessageLine("hi"), assistantLine("  ")}, nil, false)
	if len(es) != 1 {
		t.Fatalf("want only the user entry, got %v", entryKinds(es))
	}
}

func TestParseRolloutSetsPreviewAndPatchError(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"timestamp":"2026-10-05T10:00:00.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"apply_patch","input":"*** Begin Patch\n*** Update File: /r/a.go\n@@\n+x\n*** End Patch\n"}}` + "\n" +
		`{"timestamp":"2026-10-05T10:00:01.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"Exit code: 1\nWall time: 0.1 seconds\nOutput:\nverification failed\n"}}` + "\n" +
		`{"timestamp":"2026-10-05T10:00:02.000Z","type":"response_item","payload":{"type":"function_call","call_id":"c2","name":"view_image","arguments":"{\"path\":\"/tmp/x.png\",\"detail\":\"high\"}"}}` + "\n" +
		`{"timestamp":"2026-10-05T10:00:03.000Z","type":"response_item","payload":{"type":"web_search_call","id":"ws1","status":"completed","action":{"type":"search","query":"argus","queries":["argus"]}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]transcript.Entry{}
	for _, e := range entries {
		got[e.ToolName] = e
	}
	if it := got["apply_patch"]; it.InputPreview != "/r/a.go" || !it.ResultIsError {
		t.Errorf("apply_patch = preview %q err %v, want /r/a.go true", it.InputPreview, it.ResultIsError)
	}
	if it := got["view_image"]; it.InputPreview != "/tmp/x.png" {
		t.Errorf("view_image preview = %q", it.InputPreview)
	}
	if it := got["web_search"]; it.InputPreview != "argus" {
		t.Errorf("web_search preview = %q", it.InputPreview)
	}
}

// An accepted async question reads inline as assistant text (as in the Codex
// TUI); a call Codex rejected for bad arguments is a failed tool call.
func TestParseRolloutAsyncQuestionInlineAndRejectedCallFails(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"timestamp":"2026-10-05T10:00:00.000Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"request_user_input_async","arguments":"{\"questions\":[{\"title\":\"Curiosity\",\"question\":\"What?\",\"options\":[\"A\"]}]}"}}` + "\n" +
		`{"timestamp":"2026-10-05T10:00:01.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"failed to parse function arguments: unknown field ` + "`question`" + `, expected ` + "`title`" + ` or ` + "`options`" + ` at line 1 column 45"}}` + "\n" +
		`{"timestamp":"2026-10-05T10:00:02.000Z","type":"response_item","payload":{"type":"function_call","call_id":"c2","name":"request_user_input_async","arguments":"{\"questions\":[{\"title\":\"Pick a color\",\"options\":[\"Red\",\"Blue\"]},{\"title\":\"Pick a size\",\"options\":[\"S\"]}]}"}}` + "\n" +
		`{"timestamp":"2026-10-05T10:00:03.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c2","output":"{\"accepted\":true}"}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, true)
	if err != nil {
		t.Fatal(err)
	}
	var items []transcript.Entry
	var footer transcript.Entry
	for _, e := range entries {
		if e.Kind == transcript.EntryTurnEnd {
			footer = e
			continue
		}
		items = append(items, e)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want 2", items)
	}
	if footer.ToolCount != 1 {
		t.Errorf("turn footer ToolCount = %d, want 1 (the inlined question is text, not a tool call)", footer.ToolCount)
	}
	failed, asked := items[0], items[1]
	if failed.Kind != transcript.EntryTool || !failed.ResultIsError {
		t.Errorf("rejected call = kind %v err %v, want a failed tool call", failed.Kind, failed.ResultIsError)
	}
	want := "**Pick a color**\n\n- Red\n- Blue\n\n**Pick a size**\n\n- S"
	if asked.Kind != transcript.EntryText || asked.Text != want || asked.ToolName != "" {
		t.Errorf("accepted async question = kind %v tool %q text %q, want text %q", asked.Kind, asked.ToolName, asked.Text, want)
	}
}

// An async question accepted after its turn already ended (a user message came
// in between) adjusts that turn's footer, not the next one.
func TestParseRolloutAsyncAcceptedAcrossTurnBoundary(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-10-05T10:00:00.000Z","type":"response_item","payload":{"type":"function_call","call_id":"q1","name":"request_user_input_async","arguments":"{\"questions\":[{\"title\":\"Pick\",\"options\":[\"A\"]}]}"}}`,
		`{"timestamp":"2026-10-05T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}}`,
		`{"timestamp":"2026-10-05T10:00:02.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"q1","output":"{\"accepted\":true}"}}`,
		`{"timestamp":"2026-10-05T10:00:03.000Z","type":"response_item","payload":{"type":"function_call","call_id":"c2","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}}`,
	}
	var rl []rolloutLine
	for _, l := range lines {
		var r rolloutLine
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		rl = append(rl, r)
	}
	var footers []int
	for _, e := range foldRollout(rl, nil, true) {
		if e.Kind == transcript.EntryTurnEnd {
			footers = append(footers, e.ToolCount)
		}
	}
	if !reflect.DeepEqual(footers, []int{0, 1}) {
		t.Errorf("footer tool counts = %v, want [0 1]", footers)
	}
}
