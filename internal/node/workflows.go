package node

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// workflowPollInterval is how often the node re-reads live sessions' Workflow
// runs.
const workflowPollInterval = 2 * time.Second

// workflowWatcher follows each live session's Workflow launches. The watch
// loop alone refreshes trackers; mu guards the map and each track's refs, what
// the loop last read, for handlers. subs follows the subagent files that pages
// opened from a trace poll.
type workflowWatcher struct {
	mu     sync.Mutex
	tracks map[string]*workflowTrack // session id

	subsMu sync.Mutex
	subs   map[subTrackKey]adapter.WorkflowTracker
}

type subTrackKey struct{ session, path, agent string }

type workflowTrack struct {
	path    string
	tracker adapter.WorkflowTracker
	refs    []transcript.WorkflowRef
}

func newWorkflowWatcher() *workflowWatcher {
	return &workflowWatcher{
		tracks: map[string]*workflowTrack{},
		subs:   map[subTrackKey]adapter.WorkflowTracker{},
	}
}

// ref returns the last-read launch of runID in the session's transcript path.
func (w *workflowWatcher) ref(sessionID, path, runID string) (transcript.WorkflowRef, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.tracks[sessionID]
	if !ok || t.path != path {
		return transcript.WorkflowRef{}, false
	}
	for _, r := range t.refs {
		if r.RunID == runID {
			return r, true
		}
	}
	return transcript.WorkflowRef{}, false
}

// track returns the session's track for path, replacing one for another path
// (created reports that).
func (w *workflowWatcher) track(ws adapter.WorkflowSource, sessionID, path string) (t *workflowTrack, created bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t = w.tracks[sessionID]
	if t == nil || t.path != path {
		t = &workflowTrack{path: path, tracker: ws.NewWorkflowTracker(path, "")}
		w.tracks[sessionID] = t
		created = true
	}
	return t, created
}

func (w *workflowWatcher) setRefs(t *workflowTrack, refs []transcript.WorkflowRef) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t.refs = refs
}

// subRef finds runID in a subagent file, through a tracker kept per file so
// each poll reads only appended lines.
func (w *workflowWatcher) subRef(ws adapter.WorkflowSource, sessionID, path, agentID, runID string) (transcript.WorkflowRef, bool, error) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	k := subTrackKey{sessionID, path, agentID}
	t := w.subs[k]
	if t == nil {
		t = ws.NewWorkflowTracker(path, agentID)
		w.subs[k] = t
	}
	refs, err := t.Refresh()
	if err != nil {
		return transcript.WorkflowRef{}, false, err
	}
	for _, r := range refs {
		if r.RunID == runID {
			return r, true, nil
		}
	}
	return transcript.WorkflowRef{}, false, nil
}

// dropSubs forgets the session's subagent trackers not under keepPath.
func (w *workflowWatcher) dropSubs(sessionID, keepPath string) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	for k := range w.subs {
		if k.session == sessionID && k.path != keepPath {
			delete(w.subs, k)
		}
	}
}

// dropExcept forgets the sessions not in live, returning their ids.
func (w *workflowWatcher) dropExcept(live map[string]bool) []string {
	w.mu.Lock()
	var gone []string
	for id := range w.tracks {
		if !live[id] {
			delete(w.tracks, id)
			gone = append(gone, id)
		}
	}
	w.mu.Unlock()
	for _, id := range gone {
		w.dropSubs(id, "")
	}
	return gone
}

// watchWorkflowsLoop keeps each live session's running workflows current, so
// clients see them without viewing the transcript.
func (d *Node) watchWorkflowsLoop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refreshWorkflows()
		}
	}
}

// refreshWorkflows is one watch pass. Only the watch loop (or a test) calls it.
func (d *Node) refreshWorkflows() {
	w := d.workflows
	live := map[string]bool{}
	for _, s := range d.reg.Snapshot() {
		if s.Status == session.StatusDead || s.Offline || s.TranscriptPath == "" {
			continue
		}
		ws, ok := d.adapters[s.Agent].(adapter.WorkflowSource)
		if !ok {
			continue
		}
		live[s.ID] = true
		t, created := w.track(ws, s.ID, s.TranscriptPath)
		if created {
			w.dropSubs(s.ID, s.TranscriptPath)
		}
		refs, err := t.tracker.Refresh()
		if err != nil {
			d.log.Debug("workflow refresh", "session", s.ID, "err", err)
			continue
		}
		w.setRefs(t, refs)
		d.reg.SetWorkflows(s.ID, runningWorkflows(ws, s.TranscriptPath, refs, d.reg.ProcStart(s.ID)))
	}
	for _, id := range w.dropExcept(live) {
		d.reg.SetWorkflows(id, nil)
	}
}

// runningWorkflows lists the local runs still running, nil when none.
func runningWorkflows(ws adapter.WorkflowSource, root string, refs []transcript.WorkflowRef, procStart time.Time) []session.RunningWorkflow {
	var out []session.RunningWorkflow
	for _, ref := range refs {
		if ref.Remote || ref.RunID == "" || workflowStatus(ref, procStart) != "running" {
			continue
		}
		rw := session.RunningWorkflow{RunID: ref.RunID, Name: ref.Name, Since: utcRFC3339(ref.LaunchedAt)}
		if v, err := ws.ReadWorkflow(root, ref); err == nil {
			for _, p := range v.Phases {
				for _, a := range p.Agents {
					rw.Total++
					if a.State == "done" {
						rw.Done++
					}
				}
			}
		}
		out = append(out, rw)
	}
	return out
}

func utcRFC3339(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format(time.RFC3339)
}

// workflowStatus is ref's status, with a run still "running" but launched
// before the agent process started read as "stopped": the process that ran it
// has exited, so it can never finish.
func workflowStatus(ref transcript.WorkflowRef, procStart time.Time) string {
	if ref.Status != "running" || procStart.IsZero() {
		return ref.Status
	}
	at, err := time.Parse(time.RFC3339, ref.LaunchedAt)
	if err != nil || !at.Before(procStart) {
		return ref.Status
	}
	return "stopped"
}

// markStaleWorkflows applies workflowStatus to the workflow entries. It copies
// the slice and the changed refs, never mutating entries (shared with the
// transcript cache); with nothing to change it returns entries itself.
func markStaleWorkflows(entries []transcript.Entry, procStart time.Time) []transcript.Entry {
	out := entries
	copied := false
	for i, e := range entries {
		if e.Kind != transcript.EntryWorkflow || e.Workflow == nil {
			continue
		}
		status := workflowStatus(*e.Workflow, procStart)
		if status == e.Workflow.Status {
			continue
		}
		if !copied {
			out, copied = slices.Clone(entries), true
		}
		ref := *e.Workflow
		ref.Status = status
		out[i].Workflow = &ref
	}
	return out
}

// staleWorkflowMarker memoizes markStaleWorkflows across poll ticks, so an
// unchanged fold keeps returning one slice and diffEntries' identity check holds.
type staleWorkflowMarker struct {
	in, out   []transcript.Entry
	procStart time.Time
}

func (m *staleWorkflowMarker) mark(entries []transcript.Entry, procStart time.Time) []transcript.Entry {
	if len(entries) > 0 && len(entries) == len(m.in) && &entries[0] == &m.in[0] && procStart.Equal(m.procStart) {
		return m.out
	}
	m.in, m.procStart = entries, procStart
	m.out = markStaleWorkflows(entries, procStart)
	return m.out
}

func unknownWorkflow(runID string) error {
	return &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown workflow: " + runID}
}

func (d *Node) handleSessionWorkflow(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.WorkflowParams](params)
	if err != nil {
		return nil, err
	}
	if p.RunID == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "run_id required"}
	}
	s, ok := d.reg.Get(p.SessionID)
	if !ok {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "unknown session: " + p.SessionID}
	}
	if s.TranscriptPath == "" {
		return nil, &api.RPCError{Code: api.CodeInvalidRequest, Message: "session has no transcript: " + p.SessionID}
	}
	ws, ok := d.adapterFor(s.Agent).(adapter.WorkflowSource)
	if !ok {
		return nil, unknownWorkflow(p.RunID)
	}
	var ref transcript.WorkflowRef
	var found bool
	if p.AgentID != "" {
		ref, found, err = d.workflows.subRef(ws, s.ID, s.TranscriptPath, p.AgentID, p.RunID)
	} else if ref, found = d.workflows.ref(s.ID, s.TranscriptPath, p.RunID); !found {
		ref, found, err = ws.FindWorkflow(s.TranscriptPath, "", p.RunID, false)
	}
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, unknownWorkflow(p.RunID)
	}
	ref.Status = workflowStatus(ref, d.reg.ProcStart(s.ID))
	return ws.ReadWorkflow(s.TranscriptPath, ref)
}

func (d *Node) handleHistoryWorkflow(_ context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.HistoryWorkflowParams](params)
	if err != nil {
		return nil, err
	}
	if p.TranscriptPath == "" {
		return nil, fmt.Errorf("historyWorkflow: transcript_path is required")
	}
	ws, ok := d.adapterFor(p.Agent).(adapter.WorkflowSource)
	if !ok {
		return nil, unknownWorkflow(p.RunID)
	}
	v, found, err := adapter.ReadHistoryWorkflow(ws, p.TranscriptPath, p.AgentID, p.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, unknownWorkflow(p.RunID)
	}
	return v, nil
}
