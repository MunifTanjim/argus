package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

const workflowFixture = "parser/testdata/workflow-parent.jsonl"

func workflowEntry(t *testing.T, entries []Entry, toolID string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.ToolID == toolID {
			if e.Kind != EntryWorkflow {
				t.Fatalf("entry %s kind = %q, want %q", toolID, e.Kind, EntryWorkflow)
			}
			if e.Workflow == nil {
				t.Fatalf("entry %s has no Workflow ref", toolID)
			}
			return e
		}
	}
	t.Fatalf("no entry for tool %s", toolID)
	return Entry{}
}

func TestFoldWorkflowEntry(t *testing.T) {
	view, err := ReadTranscriptView(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	e := workflowEntry(t, view.Entries, "toolu_vrtx_01NVoG3FTViD1darMHQ1qjcH")
	if e.ToolName != "Workflow" {
		t.Errorf("ToolName = %q, want Workflow", e.ToolName)
	}
	w := e.Workflow
	if w.RunID != "wf_fd74c488-8d1" || w.Status != "completed" || w.Name != "final-branch-review" {
		t.Errorf("Workflow = %+v", *w)
	}
	if w.LaunchedAt != "2026-11-23T02:30:41Z" {
		t.Errorf("LaunchedAt = %q", w.LaunchedAt)
	}
	if !e.IsToolCall() {
		t.Error("workflow entry must count as a tool call")
	}
}

func TestFoldWorkflowUnfinishedLiveVsHistory(t *testing.T) {
	const toolID = "toolu_vrtx_01BBBBBBBBBBBBBBBBBBBBBB"
	live, err := ReadTranscriptView(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	if s := workflowEntry(t, live.Entries, toolID).Workflow.Status; s != "running" {
		t.Errorf("live status = %q, want running", s)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "projects", "p", "s.jsonl")
	copyFile(t, workflowFixture, path)
	hist, err := ReadHistoryTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := workflowEntry(t, hist.Entries, toolID).Workflow.Status; s != "stopped" {
		t.Errorf("history status = %q, want stopped", s)
	}
}

func TestFoldWorkflowPendingResult(t *testing.T) {
	lines := readLines(t, workflowFixture)
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(lines[0]+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	view, err := ReadTranscriptView(path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflowEntry(t, view.Entries, "toolu_vrtx_01NVoG3FTViD1darMHQ1qjcH").Workflow
	if w.RunID != "" || w.Status != "running" {
		t.Errorf("Workflow = %+v, want no run id and running", *w)
	}
}

func TestFoldWorkflowFailedAndRemote(t *testing.T) {
	view, err := ReadTranscriptView(workflowFixture)
	if err != nil {
		t.Fatal(err)
	}
	if s := workflowEntry(t, view.Entries, "toolu_vrtx_01CCCCCCCCCCCCCCCCCCCCCC").Workflow.Status; s != "failed" {
		t.Errorf("error launch status = %q, want failed", s)
	}
	if w := workflowEntry(t, view.Entries, "toolu_vrtx_01DDDDDDDDDDDDDDDDDDDDDD").Workflow; !w.Remote {
		t.Errorf("remote launch Remote = false: %+v", *w)
	}
}

func TestResolveWorkflowRefUsesSummaryFinal(t *testing.T) {
	root := "parser/testdata/workflow-parent.jsonl"
	l := parser.WorkflowLaunch{ToolID: "t", RunID: "wf_done-1"}
	if s := resolveWorkflowRef(l, root, false).Status; s != "completed" {
		t.Errorf("status = %q, want completed (summary JSON)", s)
	}
	l.Remote = true
	if s := resolveWorkflowRef(l, root, false).Status; s != "running" {
		t.Errorf("remote status = %q, want running (summary not consulted)", s)
	}
	l.End = "failed"
	if s := resolveWorkflowRef(l, root, true).Status; s != "failed" {
		t.Errorf("status = %q, want failed (end record wins)", s)
	}

	tmp := t.TempDir()
	killedRoot := filepath.Join(tmp, "s.jsonl")
	summary := filepath.Join(tmp, "s", "workflows", "wf_k.json")
	if err := os.MkdirAll(filepath.Dir(summary), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summary, []byte(`{"runId":"wf_k","status":"killed"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if s := resolveWorkflowRef(parser.WorkflowLaunch{RunID: "wf_k"}, killedRoot, false).Status; s != "stopped" {
		t.Errorf("summary killed status = %q, want stopped", s)
	}

	// A non-terminal summary status defers to the session state.
	runningSummary := filepath.Join(tmp, "s", "workflows", "wf_r.json")
	if err := os.WriteFile(runningSummary, []byte(`{"runId":"wf_r","status":"running"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if s := resolveWorkflowRef(parser.WorkflowLaunch{RunID: "wf_r"}, killedRoot, true).Status; s != "stopped" {
		t.Errorf("finished session, summary running: status = %q, want stopped", s)
	}
	if s := resolveWorkflowRef(parser.WorkflowLaunch{RunID: "wf_r"}, killedRoot, false).Status; s != "running" {
		t.Errorf("live session, summary running: status = %q, want running", s)
	}
}

func TestFindToolDetailWorkflow(t *testing.T) {
	d, ok, err := FindToolDetail(workflowFixture, "", "toolu_vrtx_01NVoG3FTViD1darMHQ1qjcH")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if d.ToolInput == "" || d.Result == "" {
		t.Errorf("detail = %+v, want input and result", d)
	}
}

func TestStreamingTranscriptMatchesOneShotWithWorkflow(t *testing.T) {
	lines := readLines(t, workflowFixture)
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	st := NewStreamingTranscript(path, path, false)
	for i, line := range lines {
		appendLine(t, path, line)
		got, err := st.Refresh()
		if err != nil {
			t.Fatalf("step %d Refresh: %v", i, err)
		}
		want, err := ReadStreamingView(path)
		if err != nil {
			t.Fatalf("step %d ReadStreamingView: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: mismatch after appending %q\n incremental = %+v\n one-shot    = %+v", i, line, got, want)
		}
	}
	final, err := st.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if s := workflowEntry(t, final, "toolu_vrtx_01NVoG3FTViD1darMHQ1qjcH").Workflow.Status; s != "completed" {
		t.Errorf("final status = %q, want completed", s)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return splitLines(b)
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0644); err != nil {
		t.Fatal(err)
	}
}
