package claudecode

import (
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
)

var _ adapter.WorkflowSource = (*ccAdapter)(nil)

func TestCCWorkflowTracker(t *testing.T) {
	tr := (&ccAdapter{}).NewWorkflowTracker(workflowFixture)
	refs, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"wf_fd74c488-8d1": "completed",
		"wf_bbbbbbbb-001": "running",
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.RunID] = r.Status
	}
	for id, st := range want {
		if got[id] != st {
			t.Errorf("run %s status = %q, want %q", id, got[id], st)
		}
	}
	if refs[0].RunID != "wf_fd74c488-8d1" {
		t.Errorf("first ref = %+v, want launch order", refs[0])
	}
	again, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refs, again) {
		t.Errorf("second Refresh = %+v, want %+v", again, refs)
	}
}

func TestCCReadWorkflow(t *testing.T) {
	a := &ccAdapter{}
	refs, _ := a.NewWorkflowTracker(workflowFixture).Refresh()
	var ref = refs[0]
	for _, r := range refs {
		if r.RunID == "wf_fd74c488-8d1" {
			ref = r
		}
	}
	view, ok, err := a.ReadWorkflow(workflowFixture, ref)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if view.RunID != ref.RunID || view.Status != "completed" {
		t.Errorf("view ref = %+v", view.WorkflowRef)
	}
	if len(view.Phases) != 1 || len(view.Phases[0].Agents) != 7 {
		t.Fatalf("phases = %+v, want 1 phase of 7 agents", view.Phases)
	}
	traces := 0
	for _, ag := range view.Phases[0].Agents {
		if ag.HasTrace {
			traces++
		}
	}
	if traces != 1 {
		t.Errorf("agents with trace = %d, want 1", traces)
	}

	ref.Remote = true
	if _, ok, _ := a.ReadWorkflow(workflowFixture, ref); ok {
		t.Error("remote ref must read as unknown")
	}
}

func TestCCFindWorkflowHistory(t *testing.T) {
	a := &ccAdapter{}
	ref, ok, err := a.FindWorkflow(workflowFixture, "", "wf_bbbbbbbb-001", true)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ref.Status != "stopped" {
		t.Errorf("history status = %q, want stopped", ref.Status)
	}
	ref, ok, _ = a.FindWorkflow(workflowFixture, "", "wf_bbbbbbbb-001", false)
	if !ok || ref.Status != "running" {
		t.Errorf("live ref = %+v ok=%v, want running", ref, ok)
	}
	if _, ok, _ := a.FindWorkflow(workflowFixture, "", "wf_nope", true); ok {
		t.Error("unknown run found")
	}
	if _, ok, _ := a.FindWorkflow(workflowFixture, "missing", "wf_bbbbbbbb-001", true); ok {
		t.Error("missing subagent file must not match")
	}
}
