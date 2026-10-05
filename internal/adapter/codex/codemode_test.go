package codex

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// parseLines folds rollout lines and returns the entries other than the
// trailing turn footer.
func parseLines(t *testing.T, lines ...string) []transcript.Entry {
	t.Helper()
	p := t.TempDir() + "/r.jsonl"
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, true)
	if err != nil {
		t.Fatal(err)
	}
	var items []transcript.Entry
	for _, e := range entries {
		if e.Kind != transcript.EntryTurnEnd {
			items = append(items, e)
		}
	}
	return items
}

const (
	cmExec      = `{"timestamp":"2026-10-05T10:00:00.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"x1","name":"exec","input":"const r = await tools.exec_command({cmd:\"touch f\", workdir:\"/w\"});\ntext(r.output);"}}`
	cmNested    = `{"timestamp":"2026-10-05T10:00:01.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-1","command":["/usr/bin/zsh","-lc","touch f"],"cwd":"file:///w","parsed_cmd":[{"type":"unknown","cmd":"touch f"}],"source":"unified_exec_startup","status":"completed","aggregated_output":"done\n","exit_code":3,"duration":{"secs":1,"nanos":500000000}}}}`
	cmOK        = `{"timestamp":"2026-10-05T10:00:02.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x1","output":[{"type":"input_text","text":"Script completed\nWall time 0.2 seconds\nOutput:\n"},{"type":"input_text","text":"done"}]}}`
	cmFailed    = `{"timestamp":"2026-10-05T10:00:02.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x1","output":[{"type":"input_text","text":"Script failed\nWall time 0.2 seconds\nOutput:\n"},{"type":"input_text","text":"Script error: boom"}]}}`
	cmUserShell = `{"timestamp":"2026-10-05T09:59:00.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"u1","command":["/usr/bin/zsh","-lc","echo hi"],"cwd":"file:///w","parsed_cmd":[{"type":"unknown","cmd":"echo hi"}],"source":"user_shell","status":"completed","aggregated_output":"hi\n","exit_code":0}}}`
)

func TestCodeModeNestedCommandReplacesExec(t *testing.T) {
	items := parseLines(t, cmExec, cmNested, cmOK)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want only the nested command", items)
	}
	it := items[0]
	if it.Kind != transcript.EntryTool || it.ToolName != "exec_command" || it.InputPreview != "touch f" {
		t.Fatalf("nested row = %+v", it)
	}
	if !strings.Contains(it.ToolInput, `"cmd":"touch f"`) || !strings.Contains(it.ToolInput, `"workdir":"/w"`) {
		t.Errorf("ToolInput = %s", it.ToolInput)
	}
	if !strings.Contains(it.Result, "Process exited with code 3") || !strings.Contains(it.Result, "Wall time: 1.5000 seconds") ||
		!strings.HasSuffix(it.Result, "Output:\ndone\n") {
		t.Errorf("Result = %q", it.Result)
	}
	if !it.ResultIsError {
		t.Error("non-zero exit code must flag the row as an error")
	}
}

// The command row takes the exec row's place, so a failed script's error row
// follows its commands.
func TestCodeModeFailedExecKeepsBothRows(t *testing.T) {
	items := parseLines(t, cmExec, cmNested, cmFailed)
	if len(items) != 2 || items[0].ToolName != "exec_command" || items[1].ToolName != "exec" {
		t.Fatalf("items = %+v, want exec_command then exec", items)
	}
	if !items[1].ResultIsError || !strings.Contains(items[1].Result, "Script error: boom") {
		t.Errorf("failed script row = %+v, want the error result", items[1])
	}
}

func TestCodeModeExecWithoutNestedKeepsSummary(t *testing.T) {
	exec := `{"timestamp":"2026-10-05T10:00:00.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"x2","name":"exec","input":"const r = await tools.exec_command({cmd:\"sleep 30\",workdir:\"/tmp\",yield_time_ms:30000}); text(r.output);"}}`
	out := `{"timestamp":"2026-10-05T10:00:01.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x2","output":"aborted by user after 1.0s"}}`
	items := parseLines(t, exec, out)
	if len(items) != 1 || items[0].ToolName != "exec" || items[0].InputPreview != "exec_command: sleep 30" {
		t.Fatalf("items = %+v, want exec row with summary", items)
	}
}

func TestCodeModeSummaryListsParallelTools(t *testing.T) {
	js := "const [time, files] = await Promise.all([\n  tools.clock__curr_time({}),\n  tools.exec_command({cmd: 'pwd && rg --files'}),\n]);"
	if got := codeModePreview(js); got != "clock__curr_time, exec_command: pwd && rg --files" {
		t.Errorf("preview = %q", got)
	}
	if got := codeModePreview("\ntext(ALL_TOOLS.map(x => x.name).join(\"\\n\"));"); got != `text(ALL_TOOLS.map(x => x.name).join("\n"));` {
		t.Errorf("no-tool preview = %q", got)
	}
}

func TestCodeModeUserShellOutsideExecUnaffected(t *testing.T) {
	items := parseLines(t, cmUserShell, cmExec, cmOK)
	if len(items) != 1 || items[0].ToolName != "exec" {
		t.Fatalf("items = %+v, want just the exec row (user_shell event outside exec ignored)", items)
	}
}

// A script that also calls tools Codex does not record keeps its exec row, so
// those calls stay visible next to the recorded command.
func TestCodeModeExecWithUnrecordedToolKept(t *testing.T) {
	exec := `{"timestamp":"2026-10-05T10:00:00.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"x1","name":"exec","input":"await Promise.all([tools.clock__curr_time({}), tools.exec_command({cmd:\"touch f\"})]);"}}`
	items := parseLines(t, exec, cmNested, cmOK)
	if len(items) != 2 || items[0].ToolName != "exec" || items[0].InputPreview != "clock__curr_time, exec_command: touch f" || items[1].ToolName != "exec_command" {
		t.Fatalf("items = %+v, want exec row kept plus the recorded command", items)
	}
}

func TestCodeModeDroppedExecNotCountedInFooter(t *testing.T) {
	p := t.TempDir() + "/r.jsonl"
	if err := os.WriteFile(p, []byte(cmExec+"\n"+cmNested+"\n"+cmOK+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := parseRollout(p, true)
	if err != nil {
		t.Fatal(err)
	}
	last := entries[len(entries)-1]
	if last.Kind != transcript.EntryTurnEnd || last.ToolCount != 1 {
		t.Fatalf("footer = kind %v ToolCount %d, want turn_end with 1 (the command row only)", last.Kind, last.ToolCount)
	}
}

func foldLines(t *testing.T, finished bool, lines ...string) []transcript.Entry {
	t.Helper()
	var rl []rolloutLine
	for _, l := range lines {
		var r rolloutLine
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		rl = append(rl, r)
	}
	return foldRollout(rl, nil, finished)
}

// Streaming re-folds only grow or update entries in place: the exec row shows
// while the script runs, its first command takes over that slot (same ID), and
// nothing disappears when the script's output arrives.
func TestCodeModeStreamingKeepsIDsStable(t *testing.T) {
	running := foldLines(t, false, cmExec)
	if len(running) != 1 || running[0].ToolName != "exec" || running[0].ID != "0" {
		t.Fatalf("while running = %+v, want the exec row", running)
	}
	ran := foldLines(t, false, cmExec, cmNested)
	done := foldLines(t, false, cmExec, cmNested, cmOK)
	for _, fold := range [][]transcript.Entry{ran, done} {
		if len(fold) != 1 || fold[0].ID != "0" || fold[0].ToolName != "exec_command" || fold[0].ToolID != "exec-1" {
			t.Fatalf("fold = %+v, want the command row at ID 0", fold)
		}
	}
}

// Real data: a user_shell command (and a late command from an aborted script)
// can complete while another exec is open; only unified_exec_startup items
// belong to the script.
func TestCodeModeIgnoresNonScriptCommands(t *testing.T) {
	userShell := strings.Replace(cmUserShell, "2026-10-05T09:59:00.000Z", "2026-10-05T10:00:00.500Z", 1)
	items := parseLines(t, cmExec, userShell, cmOK)
	if len(items) != 1 || items[0].ToolName != "exec" {
		t.Fatalf("items = %+v, want only the exec row", items)
	}
}
