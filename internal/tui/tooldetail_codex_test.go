package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func codexItem(name, input, result string) transcript.Entry {
	return transcript.Entry{Kind: transcript.EntryTool, ToolName: name, ToolInput: input, Result: result}
}

func TestApplyPatchDetailMultiFileAndMove(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: /r/new.txt\n+hello\n*** Update File: /r/a.go\n*** Move to: /r/b.go\n@@ func main\n-old\n+new\n*** Delete File: /r/gone\n*** End Patch\n"
	out := bareTv().toolBody(codexItem("apply_patch", patch, "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nSuccess. Updated the following files:\nM /r/b.go\n"), 80)
	assertContains(t, out, "A /r/new.txt", "+hello", "M /r/a.go → /r/b.go", "@@ func main", "-old", "+new", "D /r/gone")
	assertNotContains(t, out, "*** Begin Patch", "Exit code", "Success. Updated")
}

func TestApplyPatchDetailShowsFailure(t *testing.T) {
	it := codexItem("apply_patch", "*** Begin Patch\n*** Update File: /r/a\n@@\n+x\n*** End Patch\n", "Exit code: 1\nWall time: 0.1 seconds\nOutput:\nverification failed: context not found\n")
	it.ResultIsError = true
	out := bareTv().toolBody(it, 80)
	assertContains(t, out, "Error", "verification failed: context not found")
	assertNotContains(t, out, "Wall time")
}

func TestApplyPatchDetailFallsBack(t *testing.T) {
	out := bareTv().toolBody(codexItem("apply_patch", "garbage input", ""), 80)
	assertContains(t, out, "garbage input")
}

func TestCodexExecDetail(t *testing.T) {
	out := bareTv().toolBody(codexItem("exec", "const r = await tools.exec_command({cmd:\"ls\"});\ntext(r.output);", "Script completed\nWall time 0.2 seconds\nOutput:\nREADME.md"), 80)
	assertContains(t, out, "tools.exec_command", "Script completed", "README.md")

	aborted := codexItem("exec", "text(1)", "aborted by user after 1.0s")
	aborted.ResultIsError = true
	assertContains(t, bareTv().toolBody(aborted, 80), "Error", "aborted by user after 1.0s")
}

func TestCodexQuestionDetailMarksAnswerAndNote(t *testing.T) {
	in := `{"questions":[{"id":"size","header":"Size","question":"Pick a size","options":[{"label":"Small","description":"Choose Small."},{"label":"Large","description":"Choose Large."}]}]}`
	out := ansi.Strip(bareTv().toolBody(codexItem("request_user_input", in, `{"answers":{"size":{"answers":["Large","user_note: but extra large please"]}}}`), 80))
	assertContains(t, out, "Size", "Pick a size", "○ Small", "◉ Large", "Choose Small.", "Note: but extra large please")
	assertNotContains(t, out, "user_note:", `"answers"`)
}

func TestCodexQuestionDetailUnmatchedAnswerAndNote(t *testing.T) {
	in := `{"questions":[{"id":"color","header":"Color","question":"Pick a color","options":[{"label":"Red","description":""}]}]}`
	out := ansi.Strip(bareTv().toolBody(codexItem("request_user_input", in, `{"answers":{"color":{"answers":["None of the above","user_note: green actually"]}}}`), 80))
	assertContains(t, out, "○ Red", "Answer: None of the above", "Note: green actually")
}

func TestCodexQuestionDetailPending(t *testing.T) {
	in := `{"questions":[{"id":"size","question":"Pick a size","options":[{"label":"Small"},{"label":"Large"}]}]}`
	out := ansi.Strip(bareTv().toolBody(codexItem("request_user_input", in, ""), 80))
	assertContains(t, out, "○ Small", "○ Large")
	assertNotContains(t, out, "◉")
}

func TestCodexAsyncQuestionDetail(t *testing.T) {
	out := bareTv().toolBody(codexItem("request_user_input_async", `{"questions":[{"title":"Pick a color","options":["Red","Blue"]}]}`, `{"accepted":true}`), 80)
	assertContains(t, out, "Pick a color", "• Red", "• Blue", "Asked without waiting; answered in a later message.")
	assertNotContains(t, out, "accepted")
}

func TestViewImageDetail(t *testing.T) {
	ok := bareTv().toolBody(codexItem("view_image", `{"path":"/tmp/x.png","detail":"high"}`, "[image, detail: high]"), 80)
	assertContains(t, ok, "path:", "/tmp/x.png", "detail:", "high", "image attached")
	assertNotContains(t, ok, "[image")

	bad := codexItem("view_image", `{"path":"/tmp/x.ppm"}`, "image content omitted because it could not be processed")
	bad.ResultIsError = true
	assertContains(t, bareTv().toolBody(bad, 80), "Error", "could not be processed")
}

func TestMCPDetailAndLookup(t *testing.T) {
	if got := toolDisplayName("mcp__github__create_issue"); got != "github › create_issue" {
		t.Errorf("display = %q", got)
	}
	if toolIcon("mcp__github__create_issue", false) != categoryIcon(catOther) {
		t.Error("mcp tools use the other category")
	}
	out := bareTv().toolBody(codexItem("mcp__github__create_issue", `{"title":"Bug","labels":["a","b"],"repo":"o/r"}`, `{"number":7}`), 80)
	assertContains(t, out, "title:", "Bug", "labels:", `["a","b"]`, "repo:", "Result", "number")
	if _, ok := lookupTool("mcp__server"); ok {
		t.Error("mcp name without a tool part must not resolve")
	}
}

func TestRejectedQuestionShowsError(t *testing.T) {
	for _, c := range []struct{ name, input, result string }{
		{"request_user_input", `{"questions":[{"header":"Text","id":"t","question":"What exact message?"}]}`, "request_user_input requires non-empty options for every question"},
		{"request_user_input_async", `{"questions":[{"title":"Pick","options":["A"]}]}`, "failed to parse function arguments: unknown field `question`"},
	} {
		it := codexItem(c.name, c.input, c.result)
		it.ResultIsError = true
		out := ansi.Strip(bareTv().toolBody(it, 80))
		assertContains(t, out, "Error", c.result)
		assertNotContains(t, out, "Asked without waiting")
	}
}
