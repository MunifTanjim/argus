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
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.ToolName == "exec_command" && it.Result != "" && it.ResultIsError {
				t.Errorf("exit-0 exec_command marked as error: %+v", it)
			}
		}
	}
}

func TestParseRolloutMessages(t *testing.T) {
	chunks, err := parseRollout("testdata/rollout-child.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	// child = 1 real user message + 1 assistant turn; environment_context filtered.
	var user, ai int
	for _, c := range chunks {
		switch c.Kind {
		case transcript.ChunkUser:
			user++
			if wantPrefix := "This is a reference-session subagent task"; !hasPrefix(c.Text, wantPrefix) {
				t.Fatalf("user text = %q, want prefix %q", c.Text, wantPrefix)
			}
		case transcript.ChunkAI:
			ai++
		}
	}
	if user != 1 || ai != 1 {
		t.Fatalf("want user=1 ai=1, got user=%d ai=%d", user, ai)
	}
}

func TestParseRolloutFiltersScaffolding(t *testing.T) {
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	for _, c := range chunks {
		if c.Kind != transcript.ChunkUser {
			continue
		}
		if hasPrefix(c.Text, "<environment_context>") || hasPrefix(c.Text, "<subagent_notification>") {
			t.Fatalf("scaffolding user message leaked: %q", c.Text[:40])
		}
	}
}

func TestParseRolloutTools(t *testing.T) {
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	byID := map[string]transcript.Item{}
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.Kind == transcript.ItemTool {
				byID[it.ToolID] = it
			}
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
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	thinking := 0
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.Kind == transcript.ItemThinking {
				thinking++
				if it.Text != "" {
					t.Errorf("encrypted reasoning should have empty text, got %q", it.Text)
				}
			}
		}
		if c.Kind == transcript.ChunkAI && c.Thinking != countThinking(c.Items) {
			t.Errorf("Thinking count %d != thinking items %d", c.Thinking, countThinking(c.Items))
		}
	}
	if thinking == 0 {
		t.Fatal("expected thinking items from encrypted reasoning steps")
	}
}

func countThinking(items []transcript.Item) int {
	n := 0
	for _, it := range items {
		if it.Kind == transcript.ItemThinking {
			n++
		}
	}
	return n
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
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var think string
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.Kind == transcript.ItemThinking {
				think = it.Text
			}
		}
	}
	if think != "planning the fix" {
		t.Fatalf("thinking text = %q, want %q", think, "planning the fix")
	}
}

func TestParseRolloutUsageAndContext(t *testing.T) {
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var withCtx, withDur, withUsage int
	for _, c := range chunks {
		if c.Kind != transcript.ChunkAI {
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
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var sub transcript.Item
	var found bool
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.ToolName == "spawn_agent" {
				sub, found = it, true
			}
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
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	names := map[string]transcript.ItemKind{}
	for _, c := range chunks {
		for _, it := range c.Items {
			names[it.ToolName] = it.Kind
		}
	}
	if names["wait_agent"] != transcript.ItemSubagent {
		t.Fatalf("wait_agent kind = %v, want subagent", names["wait_agent"])
	}
	if names["close_agent"] != transcript.ItemSubagent {
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
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var it transcript.Item
	var found bool
	for _, c := range chunks {
		for _, i := range c.Items {
			if i.ToolName == "apply_patch" {
				it, found = i, true
			}
		}
	}
	if !found {
		t.Fatal("no apply_patch tool item found")
	}
	if it.Kind != transcript.ItemTool {
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
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var it transcript.Item
	var found bool
	for _, c := range chunks {
		for _, i := range c.Items {
			if i.ToolName == "view_image" {
				it, found = i, true
			}
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
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	c := chunks[0]
	if c.Kind != transcript.ChunkShell {
		t.Fatalf("kind = %v, want ChunkShell", c.Kind)
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
	chunks, err := parseRolloutFromText(t, "<user_shell_command>\n<command>\nfalse\n</command>\n<result>\nExit code: 1\nDuration: 0.01 seconds\nOutput:\n\n</result>\n</user_shell_command>")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(chunks) != 1 || !chunks[0].IsError {
		t.Fatalf("chunks = %+v, want 1 chunk with IsError=true", chunks)
	}
}

func parseRolloutFromText(t *testing.T, text string) ([]transcript.Chunk, error) {
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
	return parseRollout(p)
}

func TestParseRolloutSkillLoad(t *testing.T) {
	text := "<skill>\n<name>superpowers:brainstorming</name>\n<path>/Users/muniftanjim/.codex/plugins/cache/openai-curated/superpowers/3fdeeb49/skills/brainstorming/SKILL.md</path>\n---\nname: brainstorming\ndescription: \"You MUST use this before any creative work.\"\n---\n\n# Brainstorming Ideas Into Designs\n\nHelp turn ideas into fully formed designs and specs through natural collaborative dialogue.\n</skill>"
	chunks, err := parseRolloutFromText(t, text)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	c := chunks[0]
	if c.Kind != transcript.ChunkSkill {
		t.Fatalf("kind = %v, want ChunkSkill", c.Kind)
	}
	if c.Text != "superpowers:brainstorming" {
		t.Fatalf("Text = %q, want skill name", c.Text)
	}
	if want := "/Users/muniftanjim/.codex/plugins/cache/openai-curated/superpowers/3fdeeb49/skills/brainstorming/SKILL.md"; c.Label != want {
		t.Fatalf("Label = %q, want %q", c.Label, want)
	}
	if strings.Contains(c.Detail, "---") || strings.Contains(c.Detail, "description:") {
		t.Fatalf("Detail should have frontmatter stripped, got %q", c.Detail)
	}
	if !strings.HasPrefix(c.Detail, "# Brainstorming Ideas Into Designs") {
		t.Fatalf("Detail = %q, want it to start with the body heading", c.Detail)
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
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var it transcript.Item
	var found int
	for _, c := range chunks {
		for _, i := range c.Items {
			if i.ToolName == "web_search" {
				it, found = i, found+1
			}
		}
	}
	if found != 1 {
		t.Fatalf("web_search tool items = %d, want 1 (web_search_end duplicate should be ignored)", found)
	}
	if it.Kind != transcript.ItemTool {
		t.Fatalf("web_search kind = %v, want tool", it.Kind)
	}
	if !strings.Contains(it.ToolInput, `"query":"argus"`) {
		t.Fatalf("web_search ToolInput = %q, want query", it.ToolInput)
	}
}

func TestParseRolloutWaitCloseResolveNickname(t *testing.T) {
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	const childID = "019f278e-50a5-7f83-91f2-c30e8ac18e19"
	var wait, closeIt transcript.Item
	for _, c := range chunks {
		for _, it := range c.Items {
			switch it.ToolName {
			case "wait_agent":
				wait = it
			case "close_agent":
				closeIt = it
			}
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
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":100000,"last_token_usage":{"input_tokens":60,"cached_input_tokens":0,"output_tokens":20},"total_token_usage":{"input_tokens":60}}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var got int
	for _, c := range chunks {
		if c.Kind == transcript.ChunkAI {
			got = c.Usage.Output
		}
	}
	if got != 30 {
		t.Fatalf("Usage.Output = %d, want 30 (accumulated)", got)
	}
}

func TestParseRolloutDropsEmptyAIChunk(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/r.jsonl"
	content := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"model_context_window":100000,"last_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":5},"total_token_usage":{"input_tokens":50}}}}` + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	chunks, err := parseRollout(p)
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	for _, c := range chunks {
		if c.Kind == transcript.ChunkAI {
			t.Fatalf("unexpected ChunkAI with no items emitted")
		}
	}
}

func TestParseRolloutSpawnNickname(t *testing.T) {
	chunks, err := parseRollout("testdata/rollout-parent.jsonl")
	if err != nil {
		t.Fatalf("parseRollout: %v", err)
	}
	var sub transcript.Item
	var found bool
	for _, c := range chunks {
		for _, it := range c.Items {
			if it.ToolName == "spawn_agent" {
				sub, found = it, true
			}
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
