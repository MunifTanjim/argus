package tui

import (
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func agyItem(name, input, result string) transcript.Item {
	return transcript.Item{Kind: transcript.ItemTool, ToolName: name, ToolInput: input, Result: result}
}

func assertContains(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
}

func assertNotContains(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("should not contain %q:\n%s", u, out)
		}
	}
}

func TestGrepSearchDetail(t *testing.T) {
	m := testModel()
	res := `Created At: 2026-07-04T22:12:53+06:00
Completed At: 2026-07-04T22:12:53+06:00
{"File":"/a/b.go","LineNumber":41,"LineContent":"  probe against a"}
{"File":"/a/c.go","LineNumber":9,"LineContent":"probe here"}`
	out := m.toolBody(agyItem("grep_search",
		`{"Query":"probe","SearchPath":"/a","IsRegex":false,"CaseInsensitive":true}`, res), 80)
	assertContains(t, out, `"probe"`, "in /a", "case-insensitive", "/a/b.go:41", "probe against a", "/a/c.go:9")
	assertNotContains(t, out, "Created At", "LineContent")
}

func TestListDirDetail(t *testing.T) {
	m := testModel()
	res := `Created At: x
{"name":".git","isDir":true}
{"name":"Makefile","sizeBytes":"2365"}`
	out := m.toolBody(agyItem("list_dir", `{"DirectoryPath":"/repo"}`, res), 80)
	assertContains(t, out, "/repo", ".git/", "Makefile", "2.3k")
	assertNotContains(t, out, "Created At", "sizeBytes")
}

func TestViewFileDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nCompleted At: x\nFile Path: `file:///a.go`\nTotal Lines: 21\nTotal Bytes: 17924\nShowing lines 1 to 21\nThe following code has been modified to include a line number before every line.\n1: package main\n2: func main() {}"
	out := m.toolBody(agyItem("view_file", `{"AbsolutePath":"/a.go"}`, res), 80)
	assertContains(t, out, "/a.go", "Total Lines: 21", "1: package main", "2: func main() {}")
	assertNotContains(t, out, "Created At", "File Path:", "The following code")
}

func TestWriteToFileDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nCompleted At: x\nCreated file file:///a.md with requested content.\nIf relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission."
	out := m.toolBody(agyItem("write_to_file",
		`{"TargetFile":"/a.md","Description":"make notes","CodeContent":"# Title\nbody","Overwrite":true}`, res), 80)
	assertContains(t, out, "make notes", "/a.md", "(overwrite)", "+ # Title", "+ body", "Created file")
	assertNotContains(t, out, "Created At", "proactively run terminal commands")
}

func TestReplaceFileContentDetail(t *testing.T) {
	m := testModel()
	out := m.toolBody(agyItem("replace_file_content",
		`{"TargetFile":"/a.md","Description":"edit","StartLine":6,"EndLine":11,"TargetContent":"- Item 1\n- Item 2","ReplacementContent":"- New 1\n- Item 2"}`, "Created At: x\ndiff echo"), 80)
	assertContains(t, out, "edit", "/a.md", "lines 6", "- - Item 1", "+ - New 1", "- Item 2")
	// result is just the diff echo; shown only on error
	assertNotContains(t, out, "diff echo")
}

func TestMultiReplaceFileContentDetail(t *testing.T) {
	m := testModel()
	out := m.toolBody(agyItem("multi_replace_file_content",
		`{"TargetFile":"/a.md","Description":"multi","ReplacementChunks":[{"StartLine":3,"EndLine":4,"TargetContent":"old a","ReplacementContent":"new a"},{"StartLine":13,"EndLine":14,"TargetContent":"old b","ReplacementContent":"new b"}]}`, ""), 80)
	assertContains(t, out, "/a.md", "(2 edits)", "edit 1", "edit 2", "- old a", "+ new a", "- old b", "+ new b")
}

func TestSearchWebDetail(t *testing.T) {
	m := testModel()
	res := `Created At: x
Completed At: x
The search for "q" returned the following summary:
The **answer** is here.`
	out := m.toolBody(agyItem("search_web", `{"query":"q","domain":"example.com"}`, res), 80)
	assertContains(t, out, `"q"`, "example.com", "answer")
	assertNotContains(t, out, "Created At", "returned the following summary")
}

func TestGenerateImageDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nCompleted At: x\nUsing prompt: a logo\n\nGenerated image is saved at /out.jpg.\n\n Do not output the path of this image to show to the user since the user can already see it. However, you can embed this image in artifacts for the USER's review."
	out := m.toolBody(agyItem("generate_image", `{"ImageName":"logo","AspectRatio":"1:1","Prompt":"a logo"}`, res), 80)
	assertContains(t, out, "logo", "1:1", "a logo", "Generated image is saved at /out.jpg")
	assertNotContains(t, out, "Created At", "Using prompt:", "Do not output the path")
}

func TestDefineSubagentDetail(t *testing.T) {
	m := testModel()
	out := m.toolBody(agyItem("define_subagent",
		`{"name":"helper","description":"a helper","system_prompt":"You are a helper.","enable_write_tools":true,"enable_mcp_tools":false,"enable_subagent_tools":false}`, "Created At: x\nSubagent defined."), 80)
	assertContains(t, out, "helper", "a helper", "tools: write", "System Prompt", "You are a helper.")
}

func TestManageTaskDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nCompleted At: x\nTask: t-1\nStatus: RUNNING\nLog: /l.log\nLast progress: never\n\nREMINDER: Do not call this tool again to poll."
	out := m.toolBody(agyItem("manage_task", `{"Action":"status","TaskId":"t-1"}`, res), 80)
	assertContains(t, out, "status", "t-1", "Status:", "RUNNING", "Last progress:")
	assertNotContains(t, out, "Created At", "REMINDER")
}

func TestAskQuestionDetail(t *testing.T) {
	m := testModel()
	in := `{"questions":[{"is_multi_select":false,"question":"Pick one","options":["Run all, including a, b","Only files"]}]}`
	res := "Created At: x\nCompleted At: x\nA1: Run all, including a, b"
	out := m.toolBody(agyItem("ask_question", in, res), 80)
	assertContains(t, out, "Agent is asking", "Pick one", "Run all, including a, b", "Only files", "◉")
}

func TestAskPermissionDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nCompleted At: x\nPermission for command(echo) was granted. Reason provided by agent: testing."
	out := m.toolBody(agyItem("ask_permission",
		`{"Action":"command","Target":"echo","Reason":"testing"}`, res), 80)
	assertContains(t, out, "command(echo)", "testing", "was granted")
	assertNotContains(t, out, "Created At")
}

func TestSendMessageDetail(t *testing.T) {
	m := testModel()
	out := m.toolBody(agyItem("send_message", `{"Recipient":"agent-1","Message":"hello there"}`, "Created At: x\nMessage sent."), 80)
	assertContains(t, out, "agent-1", "hello there")
}

func TestScheduleDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nTool is running as a background task with task id: t-39\nTask Description: Timer: 5s"
	out := m.toolBody(agyItem("schedule", `{"DurationSeconds":"5","Prompt":"ping","TimerCondition":"never"}`, res), 80)
	assertContains(t, out, "Timer 5s", "condition: never", "ping", "t-39")
	assertNotContains(t, out, "Created At")
}

func TestListPermissionsDetail(t *testing.T) {
	m := testModel()
	res := "Created At: x\nCompleted At: x\nYou have read and write access to:\n- /repo"
	out := m.toolBody(agyItem("list_permissions", `{}`, res), 80)
	assertContains(t, out, "read and write access", "/repo")
	assertNotContains(t, out, "Created At")
}
