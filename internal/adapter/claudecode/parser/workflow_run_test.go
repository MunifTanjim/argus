package parser

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	wfRoot  = "testdata/workflow-parent.jsonl"
	wfRunID = "wf_fd74c488-8d1"
	wfAgent = "a684b9c31f9c90f4a"
)

func TestReadWorkflowRunJournal(t *testing.T) {
	run, ok, err := ReadWorkflowRun(wfRoot, wfRunID, false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(run.Phases) != 1 || run.Phases[0].Title != "Review" {
		t.Fatalf("phases = %+v", run.Phases)
	}
	agents := run.Phases[0].Agents
	if len(agents) != 7 {
		t.Fatalf("agents = %d", len(agents))
	}
	if agents[0].ID != wfAgent || agents[0].Label != "review:pkg" || agents[0].State != "done" {
		t.Errorf("first = %+v", agents[0])
	}
	if last := agents[6]; last.Label != "review:app" || last.State != "running" {
		t.Errorf("last = %+v", last)
	}
}

func TestReadWorkflowRunEndedMarksFailed(t *testing.T) {
	run, _, _ := ReadWorkflowRun(wfRoot, wfRunID, true)
	agents := run.Phases[0].Agents
	for i, a := range agents[:6] {
		if a.State != "done" {
			t.Errorf("agent %d = %+v", i, a)
		}
	}
	if agents[6].State != "failed" {
		t.Errorf("last = %+v", agents[6])
	}
}

func TestReadWorkflowRunSummaryReplacesJournal(t *testing.T) {
	run, ok, err := ReadWorkflowRun(wfRoot, "wf_done-1", false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(run.Phases) != 2 {
		t.Fatalf("run = %+v", run)
	}
	if run.Phases[0].Title != "Plan" || len(run.Phases[0].Agents) != 1 {
		t.Errorf("plan = %+v", run.Phases[0])
	}
	build := run.Phases[1]
	if build.Title != "Build" || len(build.Agents) != 2 ||
		build.Agents[0] != (WorkflowRunAgent{ID: "abuild1", Label: "build:a", State: "done"}) ||
		build.Agents[1].State != "failed" {
		t.Errorf("build = %+v", build)
	}
}

func TestMapSummaryState(t *testing.T) {
	for in, want := range map[string]string{
		"done": "done", "running": "running", "queued": "queued", "pending": "queued",
		"failed": "failed", "error": "failed", "weird": "weird",
	} {
		if got := mapSummaryState(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestReadWorkflowRunMissing(t *testing.T) {
	_, ok, err := ReadWorkflowRun(wfRoot, "wf_nope", false)
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestReadWorkflowRunIgnoresUnknownEvents(t *testing.T) {
	base, _, _ := ReadWorkflowRun(wfRoot, wfRunID, false)
	orig, err := os.ReadFile("testdata/workflow-parent/subagents/workflows/" + wfRunID + "/journal.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	noisy := "{\"type\":\"mystery\"}\n{\"type\":\"mystery\",\"agentId\":\"x\"}\nnot json\n" + string(orig)
	f := writeTempFile(t, noisy)
	defer f.Close()
	got, err := readWorkflowJournal(f, false)
	if err != nil || !reflect.DeepEqual(got, base) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestSubagentFilePathWorkflowAgent(t *testing.T) {
	p, ok := SubagentFilePath(wfRoot, wfAgent)
	if !ok || !strings.Contains(p, "subagents/workflows/"+wfRunID+"/") {
		t.Fatalf("p=%q ok=%v", p, ok)
	}
	if _, ok := SubagentFilePath(wfRoot, "missing"); ok {
		t.Error("missing agent resolved")
	}
}

func TestWorkflowAgentFilePath(t *testing.T) {
	p, ok := WorkflowAgentFilePath(wfRoot, wfRunID, wfAgent)
	if !ok || !strings.HasSuffix(p, "subagents/workflows/"+wfRunID+"/agent-"+wfAgent+".jsonl") {
		t.Fatalf("p=%q ok=%v", p, ok)
	}
	if _, ok := WorkflowAgentFilePath(wfRoot, "wf_other", wfAgent); ok {
		t.Error("agent resolved under the wrong run")
	}
}

func TestSubagentFilePathWorkflowAgentGlobCharsInRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "p[1]*?", "s.jsonl")
	dir := filepath.Join(strings.TrimSuffix(root, ".jsonl"), "subagents", "workflows", "wf_x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-a1.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if p, ok := SubagentFilePath(root, "a1"); !ok || filepath.Dir(p) != dir {
		t.Fatalf("p=%q ok=%v", p, ok)
	}
}

func TestSpawnDepthWorkflowAgent(t *testing.T) {
	if d := SpawnDepth(wfRoot, wfAgent); d != 1 {
		t.Errorf("depth = %d", d)
	}
}

func writeTempFile(t *testing.T, content string) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "journal.jsonl")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestReadWorkflowFinal(t *testing.T) {
	if got, err := ReadWorkflowFinal(wfRoot, "wf_done-1"); err != nil || got != "completed" {
		t.Errorf("present: got %q err %v, want completed", got, err)
	}
	if got, err := ReadWorkflowFinal(wfRoot, wfRunID); err != nil || got != "" {
		t.Errorf("journal only: got %q err %v, want empty", got, err)
	}
	if got, err := ReadWorkflowFinal(wfRoot, "wf_nope"); err != nil || got != "" {
		t.Errorf("absent: got %q err %v, want empty", got, err)
	}
}
