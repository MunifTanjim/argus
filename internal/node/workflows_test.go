package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// fakeWorkflows is a WorkflowSource over in-memory refs (by transcript path,
// or path+"#"+agentID for a subagent file) and views (by run id).
type fakeWorkflows struct {
	adapter.Adapter
	mu       sync.Mutex
	refs     map[string][]transcript.WorkflowRef
	views    map[string]transcript.WorkflowView
	trackers []string
}

func (f *fakeWorkflows) setRefs(path string, refs ...transcript.WorkflowRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs[path] = refs
}

func (f *fakeWorkflows) NewWorkflowTracker(path string) adapter.WorkflowTracker {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trackers = append(f.trackers, path)
	return fakeTracker{f, path}
}

func (f *fakeWorkflows) ReadWorkflow(_ string, ref transcript.WorkflowRef) (transcript.WorkflowView, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.views[ref.RunID]
	v.WorkflowRef = ref
	return v, ok, nil
}

func (f *fakeWorkflows) FindWorkflow(root, agentID, runID string, _ bool) (transcript.WorkflowRef, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := root
	if agentID != "" {
		key += "#" + agentID
	}
	for _, r := range f.refs[key] {
		if r.RunID == runID {
			return r, true, nil
		}
	}
	return transcript.WorkflowRef{}, false, nil
}

type fakeTracker struct {
	f    *fakeWorkflows
	path string
}

func (t fakeTracker) Refresh() ([]transcript.WorkflowRef, error) {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	return append([]transcript.WorkflowRef(nil), t.f.refs[t.path]...), nil
}

func newWorkflowNode(t *testing.T) (*Node, *fakeWorkflows) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	f := &fakeWorkflows{
		Adapter: d.adapters["claude"],
		refs:    map[string][]transcript.WorkflowRef{},
		views:   map[string]transcript.WorkflowView{},
	}
	d.adapters["claude"] = f
	return d, f
}

func addWorkflowSession(t *testing.T, d *Node, path string, started bool) session.Session {
	t.Helper()
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking,
		TranscriptPath: path, Started: started,
	})
	return s
}

func threeAgentView() transcript.WorkflowView {
	return transcript.WorkflowView{Phases: []transcript.WorkflowPhase{
		{Title: "p1", Agents: []transcript.WorkflowAgent{{ID: "a1", State: "done"}, {ID: "a2", State: "running"}}},
		{Title: "p2", Agents: []transcript.WorkflowAgent{{ID: "a3", State: "running"}}},
	}}
}

func workflowsOf(t *testing.T, d *Node, id string) []session.RunningWorkflow {
	t.Helper()
	s, ok := d.reg.Get(id)
	if !ok {
		t.Fatalf("session %s gone", id)
	}
	return s.Workflows
}

func TestWorkflowWatcherPublishesRunning(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", false)
	launched := "2026-11-23T10:00:00Z"
	ref := transcript.WorkflowRef{RunID: "wf_1", Name: "build", Status: "running", LaunchedAt: launched}
	f.setRefs("/t/a.jsonl", ref)
	f.views["wf_1"] = threeAgentView()

	d.refreshWorkflows()
	want := []session.RunningWorkflow{{RunID: "wf_1", Name: "build", Done: 1, Total: 3, Since: launched}}
	if got := workflowsOf(t, d, s.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("Workflows = %+v, want %+v", got, want)
	}

	ref.Status = "completed"
	f.setRefs("/t/a.jsonl", ref)
	d.refreshWorkflows()
	if got := workflowsOf(t, d, s.ID); len(got) != 0 {
		t.Fatalf("Workflows after completion = %+v, want empty", got)
	}
}

func TestStaleWorkflowAfterRestart(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", true)
	procStart := d.reg.ProcStart(s.ID)
	if procStart.IsZero() {
		t.Fatal("ProcStart not recorded")
	}
	launched := procStart.Add(-time.Hour).UTC().Format(time.RFC3339)
	ref := transcript.WorkflowRef{RunID: "wf_1", Name: "build", Status: "running", LaunchedAt: launched}
	f.setRefs("/t/a.jsonl", ref)

	d.refreshWorkflows()
	if got := workflowsOf(t, d, s.ID); len(got) != 0 {
		t.Fatalf("stale run listed: %+v", got)
	}

	in := []transcript.Entry{
		{ID: "e0", Kind: transcript.EntryText, Text: "hi"},
		{ID: "e1", Kind: transcript.EntryWorkflow, Workflow: &ref},
	}
	out := markStaleWorkflows(in, procStart)
	if out[1].Workflow.Status != "stopped" {
		t.Fatalf("marked status = %q, want stopped", out[1].Workflow.Status)
	}
	if in[1].Workflow.Status != "running" || ref.Status != "running" {
		t.Fatal("markStaleWorkflows mutated its input")
	}
	if &out[0] == &in[0] {
		t.Fatal("markStaleWorkflows did not copy the slice")
	}

	fresh := []transcript.Entry{{ID: "e1", Kind: transcript.EntryWorkflow, Workflow: &transcript.WorkflowRef{
		RunID: "wf_2", Status: "running", LaunchedAt: procStart.Add(time.Minute).UTC().Format(time.RFC3339),
	}}}
	if got := markStaleWorkflows(fresh, procStart); &got[0] != &fresh[0] {
		t.Fatal("unchanged entries must come back as the same slice")
	}
}

func TestWorkflowWatcherSkipsRemoteAndPending(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", false)
	f.setRefs("/t/a.jsonl",
		transcript.WorkflowRef{RunID: "wf_r", Status: "running", Remote: true},
		transcript.WorkflowRef{RunID: "", Status: "running"},
	)
	d.refreshWorkflows()
	if got := workflowsOf(t, d, s.ID); len(got) != 0 {
		t.Fatalf("Workflows = %+v, want none", got)
	}
}

func TestWorkflowWatcherFollowsTranscriptSwitch(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", false)
	f.setRefs("/t/b.jsonl", transcript.WorkflowRef{RunID: "wf_b", Status: "running"})
	d.refreshWorkflows()
	d.refreshWorkflows()

	d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking, TranscriptPath: "/t/b.jsonl",
	})
	d.refreshWorkflows()
	if want := []string{"/t/a.jsonl", "/t/b.jsonl"}; !reflect.DeepEqual(f.trackers, want) {
		t.Fatalf("trackers = %v, want %v", f.trackers, want)
	}
	if got := workflowsOf(t, d, s.ID); len(got) != 1 || got[0].RunID != "wf_b" {
		t.Fatalf("Workflows = %+v, want wf_b", got)
	}
}

func TestWorkflowWatcherClearsEndedSession(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", false)
	f.setRefs("/t/a.jsonl", transcript.WorkflowRef{RunID: "wf_1", Status: "running"})
	d.refreshWorkflows()
	if len(workflowsOf(t, d, s.ID)) != 1 {
		t.Fatal("run not listed")
	}
	d.reg.ApplyHook(registry.HookUpdate{Agent: "claude", AgentSessionID: "s1", Status: session.StatusDead})
	d.refreshWorkflows()
	if got, ok := d.reg.Get(s.ID); ok && len(got.Workflows) != 0 {
		t.Fatalf("dead session still lists %+v", got.Workflows)
	}
	if _, ok := d.workflows.trackers[s.ID]; ok {
		t.Fatal("tracker kept for an ended session")
	}
	if _, ok := d.workflows.ref(s.ID, "/t/a.jsonl", "wf_1"); ok {
		t.Fatal("refs kept for an ended session")
	}
}

func callWorkflow(d *Node, method string, p any) (any, error) {
	raw, _ := json.Marshal(p)
	if method == api.MethodSessionWorkflow {
		return d.handleSessionWorkflow(context.Background(), raw)
	}
	return d.handleHistoryWorkflow(context.Background(), raw)
}

func TestHandleSessionWorkflow(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", false)
	ref := transcript.WorkflowRef{RunID: "wf_1", Name: "build", Status: "running"}
	f.setRefs("/t/a.jsonl", ref)
	f.setRefs("/t/a.jsonl#agent-x", transcript.WorkflowRef{RunID: "wf_sub", Status: "completed"})
	f.views["wf_1"] = threeAgentView()
	f.views["wf_sub"] = transcript.WorkflowView{Phases: []transcript.WorkflowPhase{}}

	// Served before the watcher ran (FindWorkflow) and after (tracker ref).
	for range 2 {
		res, err := callWorkflow(d, api.MethodSessionWorkflow, api.WorkflowParams{SessionID: s.ID, RunID: "wf_1"})
		if err != nil {
			t.Fatal(err)
		}
		want := threeAgentView()
		want.WorkflowRef = ref
		if !reflect.DeepEqual(res, want) {
			t.Fatalf("view = %+v, want %+v", res, want)
		}
		d.refreshWorkflows()
	}

	res, err := callWorkflow(d, api.MethodSessionWorkflow, api.WorkflowParams{SessionID: s.ID, AgentID: "agent-x", RunID: "wf_sub"})
	if err != nil {
		t.Fatal(err)
	}
	if v := res.(transcript.WorkflowView); v.RunID != "wf_sub" || v.Status != "completed" {
		t.Fatalf("subagent view = %+v", v)
	}

	_, err = callWorkflow(d, api.MethodSessionWorkflow, api.WorkflowParams{SessionID: s.ID, RunID: "wf_nope"})
	if rpc, ok := err.(*api.RPCError); !ok || rpc.Code != api.CodeInvalidRequest {
		t.Fatalf("unknown run err = %v, want invalid-request RPC error", err)
	}
	_, err = callWorkflow(d, api.MethodSessionWorkflow, api.WorkflowParams{SessionID: "nope", RunID: "wf_1"})
	if rpc, ok := err.(*api.RPCError); !ok || rpc.Code != api.CodeInvalidRequest {
		t.Fatalf("unknown session err = %v, want invalid-request RPC error", err)
	}
}

func TestHandleSessionWorkflowMarksStale(t *testing.T) {
	d, f := newWorkflowNode(t)
	s := addWorkflowSession(t, d, "/t/a.jsonl", true)
	launched := d.reg.ProcStart(s.ID).Add(-time.Hour).UTC().Format(time.RFC3339)
	f.setRefs("/t/a.jsonl", transcript.WorkflowRef{RunID: "wf_1", Status: "running", LaunchedAt: launched})
	res, err := callWorkflow(d, api.MethodSessionWorkflow, api.WorkflowParams{SessionID: s.ID, RunID: "wf_1"})
	if err != nil {
		t.Fatal(err)
	}
	if v := res.(transcript.WorkflowView); v.Status != "stopped" {
		t.Fatalf("status = %q, want stopped", v.Status)
	}
}

func TestHandleHistoryWorkflow(t *testing.T) {
	d, f := newWorkflowNode(t)
	ref := transcript.WorkflowRef{RunID: "wf_1", Name: "build", Status: "completed"}
	f.setRefs("/h/a.jsonl", ref)
	f.views["wf_1"] = threeAgentView()

	res, err := callWorkflow(d, api.MethodSessionsHistoryWorkflow, api.HistoryWorkflowParams{
		Agent: "claude", TranscriptPath: "/h/a.jsonl", RunID: "wf_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := threeAgentView()
	want.WorkflowRef = ref
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("view = %+v, want %+v", res, want)
	}

	_, err = callWorkflow(d, api.MethodSessionsHistoryWorkflow, api.HistoryWorkflowParams{
		Agent: "claude", TranscriptPath: "/h/a.jsonl", RunID: "wf_nope",
	})
	if rpc, ok := err.(*api.RPCError); !ok || rpc.Code != api.CodeInvalidRequest {
		t.Fatalf("unknown run err = %v, want invalid-request RPC error", err)
	}
}

func TestStaleWorkflowMarkerKeepsSliceAcrossTicks(t *testing.T) {
	procStart := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	in := []transcript.Entry{{ID: "e1", Kind: transcript.EntryWorkflow, Workflow: &transcript.WorkflowRef{
		RunID: "wf_1", Status: "running", LaunchedAt: "2026-11-23T11:00:00Z",
	}}}
	var m staleWorkflowMarker
	first := m.mark(in, procStart)
	if first[0].Workflow.Status != "stopped" {
		t.Fatalf("status = %q, want stopped", first[0].Workflow.Status)
	}
	if again := m.mark(in, procStart); &again[0] != &first[0] {
		t.Fatal("an unchanged fold must map to the same marked slice")
	}
	if later := m.mark(in, time.Time{}); later[0].Workflow.Status != "running" {
		t.Fatal("a changed process start must re-mark")
	}
}

// runningLaunch is a Claude transcript with one Workflow launch, unended and
// launched in the past (the fixture's second run and its async ack).
func runningLaunch(t *testing.T) []byte {
	t.Helper()
	fixture, err := os.ReadFile("../adapter/claudecode/parser/testdata/workflow-parent.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(fixture), "\n")
	return []byte(lines[4] + "\n" + lines[5] + "\n")
}

// TestTraceSubscriptionMarksStale: a subagent trace reads a run left running by
// an exited agent process as stopped, like the main session does.
func TestTraceSubscriptionMarksStale(t *testing.T) {
	d := newNode(nil)
	root := writeTempTranscript(t)
	subDir := filepath.Join(strings.TrimSuffix(root, ".jsonl"), "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "agent-a1.jsonl"), runningLaunch(t), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking,
		TranscriptPath: root, Started: true,
	})

	ctx, cancel := context.WithCancel(api.WithNotifier(context.Background(), &fakeNotifier{ch: make(chan api.Notification, 8)}))
	defer cancel()
	res, err := d.handleTranscriptSubscribe(ctx, mustJSON(api.TranscriptSubscribeParams{SubID: "x", SessionID: s.ID, AgentID: "a1"}))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range res.(api.TranscriptDelta).Entries {
		if e.Kind == transcript.EntryWorkflow {
			found = true
			if e.Workflow.Status != "stopped" {
				t.Fatalf("status = %q, want stopped", e.Workflow.Status)
			}
		}
	}
	if !found {
		t.Fatal("no workflow entry in the trace")
	}
}

// TestOrchTranscriptMarksStale: the MCP transcript read marks stale runs too.
func TestOrchTranscriptMarksStale(t *testing.T) {
	d := newNode(nil)
	root := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(root, runningLaunch(t), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent: "claude", AgentSessionID: "s1", Status: session.StatusWorking,
		TranscriptPath: root, Started: true,
	})
	res, err := d.transcript(context.Background(), session.CompositeID(d.id, s.ID), 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range res.Entries {
		if e.Kind == transcript.EntryWorkflow {
			found = true
			if e.Workflow.Status != "stopped" {
				t.Fatalf("status = %q, want stopped", e.Workflow.Status)
			}
		}
	}
	if !found {
		t.Fatal("no workflow entry in the transcript")
	}
}
