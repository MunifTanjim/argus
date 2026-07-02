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
	if !strings.Contains(out, "✓") {
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
	for _, glyph := range []string{"✓", "⟳", "○"} {
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
