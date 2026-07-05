package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// TestMain resolves theme colors and icons once so rendering helpers under test
// produce real glyphs/colors (initTheme/initIcons set package globals).
func TestMain(m *testing.M) {
	initTheme(true)
	initIcons()
	os.Exit(m.Run())
}

func TestToolCategoryColorsActive(t *testing.T) {
	// After initTheme, category colors must be distinct from the dim default.
	if ColorToolBash == ColorTextDim {
		t.Error("ColorToolBash should not be the dim default")
	}
	if ColorToolRead == ColorTextDim {
		t.Error("ColorToolRead should not be the dim default")
	}
}

func TestToolColorLookup(t *testing.T) {
	cases := map[string]interface{}{
		"Bash":       ColorToolBash,
		"Read":       ColorToolRead,
		"Edit":       ColorToolEdit,
		"MultiEdit":  ColorToolEdit,
		"Write":      ColorToolWrite,
		"Grep":       ColorToolGrep,
		"Glob":       ColorToolGlob,
		"Task":       ColorToolTask,
		"WebFetch":   ColorToolWeb,
		"Frobnicate": ColorToolOther,
	}
	for name, want := range cases {
		if got := toolColor(name); got != want {
			t.Errorf("toolColor(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGenericToolBodyReadableResult(t *testing.T) {
	m := testModel() // jsonHL is nil → non-JSON path
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "UnknownTool",
		ToolInput: `{"x":1}`, Result: "plain non-json result line",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Input") {
		t.Errorf("missing Input label:\n%s", out)
	}
	if !strings.Contains(out, "Result") {
		t.Errorf("missing Result label:\n%s", out)
	}
	if !strings.Contains(out, "plain non-json result line") {
		t.Errorf("result content lost:\n%s", out)
	}
}

func TestGenericToolBodyError(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "UnknownTool",
		Result: "boom", ResultIsError: true,
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Error") {
		t.Errorf("error result should use Error label:\n%s", out)
	}
}

func TestEditToolDetailShowsDiff(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "Edit",
		ToolInput: `{"file_path":"a.go","old_string":"foo","new_string":"bar"}`,
		Result:    "ok",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "foo") || !strings.Contains(out, "bar") {
		t.Errorf("edit diff should show old/new:\n%s", out)
	}
}

func TestBashDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "Bash",
		ToolInput: `{"command":"ls -la","description":"list files"}`,
		Result:    "total 5\nfile.go",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "$ ls -la") {
		t.Errorf("missing command line:\n%s", out)
	}
	if !strings.Contains(out, "list files") {
		t.Errorf("missing description:\n%s", out)
	}
	if !strings.Contains(out, "file.go") {
		t.Errorf("missing result:\n%s", out)
	}
}

func TestExecCommandDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "exec_command",
		ToolInput: `{"cmd":"pwd","workdir":"/repo","yield_time_ms":1000,"max_output_tokens":2000}`,
		Result:    "Chunk ID: abc123\nWall time: 0.0100 seconds\nProcess exited with code 0\nOriginal token count: 5\nOutput:\n/repo\n",
	}
	out := m.toolBody(it, 60)
	for _, want := range []string{
		"# /repo", "$ pwd", "yield 1000ms", "max 2000 tokens",
		"Chunk ID", "abc123", "Wall time", "0.0100 seconds", "Process exited with code 0", "Original token count", "5", "/repo",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestExecCommandDetailOutputColonNotSplit(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "exec_command",
		ToolInput: `{"cmd":"foo --help"}`,
		Result:    "Chunk ID: abc123\nWall time: 0.0100 seconds\nProcess exited with code 0\nOriginal token count: 5\nOutput:\nusage: foo [-h]\nnote: see docs\n",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "usage: foo [-h]") {
		t.Errorf("output line with a colon should render verbatim:\n%s", out)
	}
	if !strings.Contains(out, "note: see docs") {
		t.Errorf("output line with a colon should render verbatim:\n%s", out)
	}
}

func TestExecCommandDetailInputMissingCommandFallsBackToDump(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "exec_command",
		ToolInput: `{"session_id":"s1","chars":"y\n"}`,
	}
	out := m.toolBody(it, 60)
	for _, want := range []string{`"session_id"`, `"s1"`, `"chars"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestExecCommandDetailNonZeroExit(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "exec_command", ResultIsError: true,
		ToolInput: `{"cmd":"false"}`,
		Result:    "Chunk ID: abc123\nWall time: 0.0100 seconds\nProcess exited with code 1\nOriginal token count: 0\nOutput:\n",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Process exited with code 1") {
		t.Errorf("missing exit code line:\n%s", out)
	}
	if !strings.Contains(out, "Error") {
		t.Errorf("non-zero exit should label as Error:\n%s", out)
	}
}

func TestExecCommandDetailNoWorkdirOrLimits(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "exec_command",
		ToolInput: `{"cmd":"echo hi"}`,
		Result:    "hi",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "$ echo hi") {
		t.Errorf("missing command line:\n%s", out)
	}
	if !strings.Contains(out, "hi") {
		t.Errorf("missing raw result:\n%s", out)
	}
}

func TestRunCommandDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "run_command",
		ToolInput: `{"CommandLine":"git status","Cwd":"/repo","WaitMsBeforeAsync":5000,"toolSummary":"Check status"}`,
		Result:    "Created At: 2026-07-04T22:06:13+06:00\nCompleted At: 2026-07-04T22:06:16+06:00\n\n\t\t\t\tThe command completed successfully.\n\t\t\t\tOutput:\n\t\t\t\tOn branch main\nnothing to commit\n\n",
	}
	out := m.toolBody(it, 60)
	for _, want := range []string{
		"# /repo", "$ git status",
		"Created At", "Completed At", "The command completed successfully.",
		"On branch main", "nothing to commit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\t\t\t\t") {
		t.Errorf("scaffolding tab indent should be stripped:\n%s", out)
	}
}

func TestRunCommandDetailOutputColonNotSplit(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "run_command",
		ToolInput: `{"CommandLine":"foo --help","Cwd":"/repo"}`,
		Result:    "Created At: x\n\n\t\t\t\tThe command completed successfully.\n\t\t\t\tOutput:\n\t\t\t\tusage: foo [-h]\nnote: see docs\n",
	}
	out := m.toolBody(it, 60)
	for _, want := range []string{"usage: foo [-h]", "note: see docs"} {
		if !strings.Contains(out, want) {
			t.Errorf("output line with a colon should render verbatim:\n%s", out)
		}
	}
}

func TestRunCommandDetailBackgroundTaskNoOutput(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "run_command",
		ToolInput: `{"CommandLine":"agy --print hi"}`,
		Result:    "Created At: x\nTool is running as a background task with task id: t-1\nTask Description: agy --print hi\n",
	}
	out := m.toolBody(it, 60)
	for _, want := range []string{"$ agy --print hi", "Tool is running as a background task", "t-1", "Task Description"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestRunCommandDetailInputMissingCommandFallsBackToDump(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "run_command",
		ToolInput: `{"Cwd":"/repo","WaitMsBeforeAsync":5000}`,
	}
	out := m.toolBody(it, 60)
	for _, want := range []string{`"Cwd"`, `"/repo"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestRunCommandDetailNonZeroExit(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "run_command", ResultIsError: true,
		ToolInput: `{"CommandLine":"false"}`,
		Result:    "Created At: x\n\n\t\t\t\tThe command failed with exit code: 2\n\t\t\t\tOutput:\n\t\t\t\tboom\n",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "The command failed with exit code") {
		t.Errorf("missing failure line:\n%s", out)
	}
	if !strings.Contains(out, "Error") {
		t.Errorf("error result should label as Error:\n%s", out)
	}
}

func TestReadDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "Read",
		ToolInput: `{"file_path":"/repo/main.go"}`,
		Result:    "     1\tpackage main\n     2\tfunc main(){}",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "/repo/main.go") {
		t.Errorf("missing file path:\n%s", out)
	}
	if !strings.Contains(out, "package main") {
		t.Errorf("missing file content:\n%s", out)
	}
}

func TestTodoWriteDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "TodoWrite",
		ToolInput: `{"todos":[
			{"content":"do A","status":"completed","activeForm":"doing A"},
			{"content":"do B","status":"in_progress","activeForm":"doing B"},
			{"content":"do C","status":"pending","activeForm":"doing C"}
		]}`,
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, Icon.Task.Done.Glyph) {
		t.Errorf("missing done glyph:\n%s", out)
	}
	if !strings.Contains(out, "do A") {
		t.Errorf("missing completed item:\n%s", out)
	}
	if !strings.Contains(out, "doing B") {
		t.Errorf("in-progress should show activeForm:\n%s", out)
	}
	if !strings.Contains(out, "do C") {
		t.Errorf("missing pending item:\n%s", out)
	}
}

func TestTodoWriteAllGlyphs(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "TodoWrite",
		ToolInput: `{"todos":[
			{"content":"a","status":"completed"},
			{"content":"b","status":"in_progress","activeForm":"doing b"},
			{"content":"c","status":"pending"}
		]}`,
	}
	out := m.toolBody(it, 60)
	for _, glyph := range []string{Icon.Task.Done.Glyph, Icon.Task.Active.Glyph, Icon.Task.Pending.Glyph} {
		if !strings.Contains(out, glyph) {
			t.Errorf("todo list missing %q glyph:\n%s", glyph, out)
		}
	}
}

func TestTodoWriteEmptyFallsBackToGeneric(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "TodoWrite",
		ToolInput: `{"todos":[]}`, Result: "updated",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Result") || !strings.Contains(out, "updated") {
		t.Errorf("empty todos should fall back to generic body:\n%s", out)
	}
}

func TestUpdatePlanDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "update_plan",
		ToolInput: `{"plan":[
			{"step":"do A","status":"completed"},
			{"step":"do B","status":"in_progress"},
			{"step":"do C","status":"pending"}
		]}`,
	}
	out := m.toolBody(it, 60)
	for _, glyph := range []string{Icon.Task.Done.Glyph, Icon.Task.Active.Glyph, Icon.Task.Pending.Glyph} {
		if !strings.Contains(out, glyph) {
			t.Errorf("plan list missing %q glyph:\n%s", glyph, out)
		}
	}
	for _, step := range []string{"do A", "do B", "do C"} {
		if !strings.Contains(out, step) {
			t.Errorf("missing step %q:\n%s", step, out)
		}
	}
}

func TestUpdatePlanEmptyFallsBackToGeneric(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "update_plan",
		ToolInput: `{"plan":[]}`, Result: "Plan updated",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Result") || !strings.Contains(out, "Plan updated") {
		t.Errorf("empty plan should fall back to generic body:\n%s", out)
	}
}

func TestBashOutputUsesGeneric(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "BashOutput",
		ToolInput: `{"bash_id":"123"}`, Result: "streamed output",
	}
	out := m.toolBody(it, 60)
	// Generic layout shows an Input label; the command renderer would not.
	if !strings.Contains(out, "Input") {
		t.Errorf("BashOutput should use the generic Input/Result layout:\n%s", out)
	}
	if strings.Contains(out, "$ ") {
		t.Errorf("BashOutput should not render a command prompt:\n%s", out)
	}
}

func TestGrepDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "Grep",
		ToolInput: `{"pattern":"handler","glob":"*.go","path":"internal"}`,
		Result:    "internal/x.go:10:func handler()",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, `"handler"`) {
		t.Errorf("missing quoted pattern header:\n%s", out)
	}
	if strings.Contains(out, `"pattern"`) {
		t.Errorf("should not show raw JSON input:\n%s", out)
	}
	if !strings.Contains(out, "internal/x.go:10") {
		t.Errorf("missing matches:\n%s", out)
	}
}

func TestGlobDetail(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "Glob",
		ToolInput: `{"pattern":"**/*.go"}`,
		Result:    "a.go\nb.go",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "**/*.go") || !strings.Contains(out, "a.go") {
		t.Errorf("glob render wrong:\n%s", out)
	}
}

func TestParseAnsweredAnswers(t *testing.T) {
	single := `Your questions have been answered: "Pick a color"="Blue". You can now continue with these answers in mind.`
	got := parseAnsweredAnswers(single)
	if got["Pick a color"] != "Blue" {
		t.Errorf("single answer = %q, want Blue (map: %v)", got["Pick a color"], got)
	}

	multi := `Your questions have been answered: "Q one"="A1", "Q two"="A2". You can now continue.`
	got = parseAnsweredAnswers(multi)
	if got["Q one"] != "A1" || got["Q two"] != "A2" {
		t.Errorf("multi answers parsed wrong: %v", got)
	}

	if g := parseAnsweredAnswers("no pairs here"); len(g) != 0 {
		t.Errorf("garbage result should parse to empty map, got %v", g)
	}
}

func TestAskUserQuestionDetailSingle(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "AskUserQuestion",
		ToolInput: `{"questions":[{"header":"Color","question":"Pick a color",
			"multiSelect":false,"options":[
				{"label":"Red","description":"warm hue"},
				{"label":"Blue","description":"cool hue"}
			]}]}`,
		Result: `Your questions have been answered: "Pick a color"="Blue". You can now continue.`,
	}
	out := m.toolBody(it, 60)
	if strings.Contains(out, `"questions"`) || strings.Contains(out, `"multiSelect"`) {
		t.Errorf("should not dump raw JSON:\n%s", out)
	}
	for _, want := range []string{"Pick a color", "Red", "Blue", "warm hue", "cool hue"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "◉") {
		t.Errorf("chosen single-select option should show a filled radio:\n%s", out)
	}
}

func TestAskUserQuestionDetailMultiSelect(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "AskUserQuestion",
		ToolInput: `{"questions":[{"header":"Toppings","question":"Choose toppings",
			"multiSelect":true,"options":[
				{"label":"Cheese"},{"label":"Olives"},{"label":"Onions"}
			]}]}`,
		Result: `Your questions have been answered: "Choose toppings"="Cheese, Olives". Continue.`,
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "[x]") {
		t.Errorf("selected multi options should show [x]:\n%s", out)
	}
	if !strings.Contains(out, "[ ]") {
		t.Errorf("unselected multi options should show [ ]:\n%s", out)
	}
}

func TestAskUserQuestionDetailCustomAnswer(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "AskUserQuestion",
		ToolInput: `{"questions":[{"header":"Color","question":"Pick a color",
			"multiSelect":false,"options":[{"label":"Red"},{"label":"Blue"}]}]}`,
		Result: `Your questions have been answered: "Pick a color"="Chartreuse". Continue.`,
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Chartreuse") {
		t.Errorf("custom answer not matching an option should appear as a fallback line:\n%s", out)
	}
}

func TestAskUserQuestionDetailEmptyFallsBackToGeneric(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "AskUserQuestion",
		ToolInput: `{"questions":[]}`, Result: "updated",
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Result") || !strings.Contains(out, "updated") {
		t.Errorf("no questions should fall back to generic body:\n%s", out)
	}
}

func TestWebDetail(t *testing.T) {
	m := testModel()
	fetch := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "WebFetch",
		ToolInput: `{"url":"https://example.com","prompt":"summarize"}`,
		Result:    "the page says hi",
	}
	out := m.toolBody(fetch, 60)
	if !strings.Contains(out, "https://example.com") {
		t.Errorf("missing url:\n%s", out)
	}
	if !strings.Contains(out, "the page says hi") {
		t.Errorf("missing result:\n%s", out)
	}
	if strings.Contains(out, `"url"`) {
		t.Errorf("should not show raw JSON input:\n%s", out)
	}

	search := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "WebSearch",
		ToolInput: `{"query":"golang lipgloss"}`,
		Result:    "result list",
	}
	out = m.toolBody(search, 60)
	if !strings.Contains(out, "golang lipgloss") {
		t.Errorf("missing query:\n%s", out)
	}
}

func TestWaitAgentDetailShowsNicknameAndStatus(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "wait_agent",
		ToolInput: `{"targets":["019f278e-50a5-7f83-91f2-c30e8ac18e19"],"timeout_ms":30000}`,
		Result:    `{"status":{"019f278e-50a5-7f83-91f2-c30e8ac18e19":{"completed":"role=subagent, result=ok"}}}`,
		Subagents: []transcript.Subagent{
			{ID: "019f278e-50a5-7f83-91f2-c30e8ac18e19", Name: "Volta"},
		},
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Volta") {
		t.Errorf("expected nickname in output, got:\n%s", out)
	}
	if strings.Contains(out, "019f278e") {
		t.Errorf("raw agent id should not leak when nickname is known:\n%s", out)
	}
	if !strings.Contains(out, "completed") {
		t.Errorf("expected status state in output, got:\n%s", out)
	}
	if !strings.Contains(out, "role=subagent, result=ok") {
		t.Errorf("expected the agent's message in output, got:\n%s", out)
	}
	if !strings.Contains(out, "timeout 30000ms") {
		t.Errorf("expected timeout in output, got:\n%s", out)
	}
}

func TestWaitAgentDetailFallsBackToRawIDWithoutNickname(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "wait_agent",
		ToolInput: `{"targets":["agent-xyz"]}`,
		Result:    `{"status":{"agent-xyz":{"running":"still working"}}}`,
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "agent-xyz") {
		t.Errorf("expected raw id fallback when no nickname known, got:\n%s", out)
	}
}

func TestCloseAgentDetailShowsNicknameAndStatus(t *testing.T) {
	m := testModel()
	it := transcript.Item{
		Kind: transcript.ItemTool, ToolName: "close_agent",
		ToolInput: `{"target":"019f278e-50a5-7f83-91f2-c30e8ac18e19"}`,
		Result:    `{"previous_status":{"completed":"all done here"}}`,
		Subagents: []transcript.Subagent{
			{ID: "019f278e-50a5-7f83-91f2-c30e8ac18e19", Name: "Volta"},
		},
	}
	out := m.toolBody(it, 60)
	if !strings.Contains(out, "Volta") {
		t.Errorf("expected nickname in output, got:\n%s", out)
	}
	if strings.Contains(out, "019f278e") {
		t.Errorf("raw agent id should not leak when nickname is known:\n%s", out)
	}
	if !strings.Contains(out, "all done here") {
		t.Errorf("expected the agent's message in output, got:\n%s", out)
	}
}
