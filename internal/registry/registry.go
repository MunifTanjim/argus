// Package registry holds argus's live model of all known sessions and publishes
// change events to subscribers (clients, the TUI). It is tool- and
// transport-agnostic: adapters feed it discoveries and hook events; the API
// layer reads snapshots and streams events.
package registry

import (
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

// defaultEndedGrace is how long a scan is barred from re-creating a session an
// exit hook just ended. The agent is still alive while its own exit hook runs, so
// the rescan that hook triggers would otherwise rediscover the pane immediately.
const defaultEndedGrace = 5 * time.Second

// now is the registry's clock. A var so tests can fix it.
var now = time.Now

// setStatus sets s.Status and stamps StatusSince when the status changes (or
// was never stamped). Caller holds r.mu.
func setStatus(s *session.Session, st session.Status) {
	if s.Status == st && s.StatusSince != "" {
		return
	}
	s.Status = st
	s.StatusSince = now().UTC().Format(time.RFC3339)
}

// EventType describes a change to the registry.
type EventType string

const (
	EventAdded   EventType = "added"
	EventUpdated EventType = "updated"
	EventRemoved EventType = "removed"
)

// Event is a single registry change delivered to subscribers.
type Event struct {
	Type    EventType       `json:"type"`
	Session session.Session `json:"session"`

	// Replay marks an event that re-states existing state (a connect/reconnect
	// snapshot) rather than a fresh change. Gateway-internal, never sent on the wire,
	// so consumers like the push watcher record it without re-notifying.
	Replay bool `json:"-"`
}

// Registry is the concurrency-safe session store.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*session.Session // internal ID -> session
	index    *sessionIndex               // pane/agent-session correlation
	subs     map[int]chan Event
	nextSub  int

	// ended maps a pane key or agent session id an exit hook just ended to when it
	// happened.
	ended      map[string]time.Time
	endedGrace time.Duration

	// inputPolicies are per-agent input channel overrides (see SetInputPolicy).
	inputPolicies map[string]InputPolicy

	// models are the live models agents report, by session id. They win over
	// the transcript-derived Summary fields (see overlayModel).
	models map[string]liveModel

	// subagents are keyed by session id.
	subagents map[string][]session.RunningSubagent
	shells    map[string][]session.RunningShell
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		sessions:      make(map[string]*session.Session),
		index:         newSessionIndex(),
		subs:          make(map[int]chan Event),
		ended:         make(map[string]time.Time),
		endedGrace:    defaultEndedGrace,
		inputPolicies: make(map[string]InputPolicy),
		models:        make(map[string]liveModel),
		subagents:     make(map[string][]session.RunningSubagent),
		shells:        make(map[string][]session.RunningShell),
	}
}

// PaneKey identifies a pane across servers; pane ids are only unique per server,
// so the server must be part of the key.
func PaneKey(server session.TmuxServer, paneID string) string {
	return string(server) + ":" + paneID
}

// AgentSessionKey is the session id keyed by the agent's own session id. Headless
// agents (opencode) are keyed this way because their pane is a disposable viewer,
// so a pane-based id would not stay stable across adopt/respawn.
func AgentSessionKey(agent, agentSessionID string) string {
	return agent + ":" + agentSessionID
}

// Get returns a copy of the session with the given ID and whether it exists.
func (r *Registry) Get(id string) (session.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return *s, true
	}
	return session.Session{}, false
}

// FindByAgentSession returns a copy of the session indexed under the agent's own
// session id, which survives a session switch that keeps the record id.
func (r *Registry) FindByAgentSession(agentSessionID string) (session.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.index.findByAgentSession(agentSessionID); ok {
		if s := r.sessions[id]; s != nil {
			return *s, true
		}
	}
	return session.Session{}, false
}

// Snapshot returns a copy of all sessions currently tracked.
func (r *Registry) Snapshot() []session.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]session.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		cp := *s
		cp.StatusLabel = cp.Status.Label()
		out = append(out, cp)
	}
	return out
}

// SubscriberCount reports active subscribers so a caller can wait for a watcher to
// subscribe before publishing.
func (r *Registry) SubscriberCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.subs)
}

// Subscribe returns an event channel plus a cancel func. The channel is buffered;
// events are dropped for a slow subscriber rather than blocking the registry.
func (r *Registry) Subscribe() (<-chan Event, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextSub
	r.nextSub++
	ch := make(chan Event, 64)
	r.subs[id] = ch
	cancel := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if c, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(c)
		}
	}
	return ch, cancel
}

// publish delivers an event to all subscribers without blocking. Caller must
// hold r.mu.
func (r *Registry) publish(ev Event) {
	ev.Session.StatusLabel = ev.Session.Status.Label()
	for _, ch := range r.subs {
		select {
		case ch <- ev:
		default: // drop for slow subscriber
		}
	}
}

// DiscoveredSession is one reconcile input. Correlated by pane key when HasPane,
// else by agent session id.
type DiscoveredSession struct {
	AgentSessionID string
	HasPane        bool
	Server         session.TmuxServer
	PaneID         string
	SessionName    string
	WindowIndex    int
	CurrentPath    string
	Frontend       session.Frontend // adapter-computed (tmux/vscode/external)
	Name           string
	Cwd            string
	Repo           string
	TranscriptPath string
	Summary        *session.Summary
	StatusHint     session.Status
}

// ReconcileSessions syncs an agent's sessions to the scan's live set. Liveness
// is dual-or: pane OR agent session id keeps a session alive.
func (r *Registry) ReconcileSessions(agent string, found []DiscoveredSession) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweepEnded()

	seenPane := map[string]bool{}
	seenAgentSession := map[string]bool{}

	for _, f := range found {
		paneK := ""
		if f.HasPane {
			paneK = PaneKey(f.Server, f.PaneID)
			seenPane[paneK] = true
		}
		if f.AgentSessionID != "" {
			seenAgentSession[f.AgentSessionID] = true
		}

		var s *session.Session
		if paneK != "" {
			if id, ok := r.index.findByPane(paneK); ok {
				s = r.sessions[id]
			}
		}
		if s == nil && f.AgentSessionID != "" {
			if id, ok := r.index.findByAgentSession(f.AgentSessionID); ok {
				s = r.sessions[id]
			}
		}

		created := false
		if s == nil {
			if r.recentlyEnded(paneK) || r.recentlyEnded(f.AgentSessionID) {
				continue
			}
			id := paneK
			if id == "" {
				id = AgentSessionKey(agent, f.AgentSessionID)
			}
			id = r.freeID(id)
			s = &session.Session{
				ID:     id,
				Agent:  agent,
				Source: session.SourceDiscovered,
			}
			r.sessions[id] = s
			setStatus(s, session.StatusDiscovered)
			created = true
		}

		if paneK != "" {
			r.index.setPane(paneK, s.ID)
			s.Tmux.Server = f.Server
			s.Tmux.PaneID = f.PaneID
			s.Tmux.SessionName = f.SessionName
			s.Tmux.WindowIndex = f.WindowIndex
			s.Tmux.CurrentPath = f.CurrentPath
		}
		r.reindexAgentSession(s, f.AgentSessionID)

		// A pane is always tmux; otherwise adopt the discovered frontend only while
		// still paneless. Never downgrade.
		if f.HasPane {
			s.Frontend = session.FrontendTmux
		} else if s.Tmux.PaneID == "" && f.Frontend != "" {
			s.Frontend = f.Frontend
		}

		if f.Name != "" {
			s.Name = f.Name
		}
		// A pane-only record (no AgentSessionID) carries the pane's current path, not
		// an authoritative proc-session cwd — only seed it, never overwrite a value a
		// proc-session/hook already set (the user may have cd'd within the pane).
		paneOnly := f.AgentSessionID == ""
		if f.Cwd != "" && !(paneOnly && s.Cwd != "") {
			s.Cwd = f.Cwd
		}
		if f.Repo != "" && !(paneOnly && s.Repo != "") {
			s.Repo = f.Repo
		}
		setTranscriptPath(s, f.TranscriptPath)
		if f.Summary != nil {
			s.Summary = f.Summary
		}
		r.overlayModel(s)
		r.overlayRunning(s)
		r.refreshInput(s)
		applyStatusHint(s, f.StatusHint)

		evType := EventUpdated
		if created {
			evType = EventAdded
		}
		r.publish(Event{Type: evType, Session: *s})
	}

	for id, s := range r.sessions {
		if s.Agent != agent {
			continue
		}
		pk := ""
		alive := false
		if s.Tmux.PaneID != "" {
			pk = PaneKey(s.Tmux.Server, s.Tmux.PaneID)
			if seenPane[pk] {
				alive = true
			}
		}
		if s.AgentSessionID != "" && seenAgentSession[s.AgentSessionID] {
			alive = true
		}
		if !alive {
			r.remove(id, pk, session.StatusDead)
		}
	}
}

// reindexAgentSession re-keys the index when the agent session id changes.
// Caller holds r.mu.
func (r *Registry) reindexAgentSession(s *session.Session, agentSessionID string) {
	if agentSessionID == "" || s.AgentSessionID == agentSessionID {
		return
	}
	// A Claude session id belongs to one conversation, so a different session
	// already holding this id is the same conversation surfaced twice: after /clear
	// on a compacted session, a paned session adopts the id its parked background job
	// reports while the job's own paneless card lingers. Evict that duplicate — the
	// shared id would otherwise mark it alive through the sweep as a second card.
	if other, ok := r.index.findByAgentSession(agentSessionID); ok && other != s.ID {
		if dup := r.sessions[other]; dup != nil && dup.Tmux.PaneID == "" {
			r.remove(other, "", session.StatusDead)
		}
	}
	r.index.clear("", s.AgentSessionID)
	s.AgentSessionID = agentSessionID
	r.index.setAgentSession(agentSessionID, s.ID)
}

// InputPolicy decides a session's input channel in place of its pane state.
// Policies run while the registry lock is held, so they must not call back into the Registry.
type InputPolicy func(s session.Session) session.InputMode

// SetInputPolicy makes p decide the input channel of agent's sessions; nil
// removes it. Called before sessions arrive.
func (r *Registry) SetInputPolicy(agent string, p InputPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == nil {
		delete(r.inputPolicies, agent)
		return
	}
	r.inputPolicies[agent] = p
}

// RefreshInput re-derives the input channel of the session with agentSessionID,
// publishing an update when it changed.
func (r *Registry) RefreshInput(agentSessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.index.findByAgentSession(agentSessionID)
	if !ok {
		return
	}
	s := r.sessions[id]
	if s == nil {
		return
	}
	before := s.Input
	r.refreshInput(s)
	if s.Input != before {
		r.publish(Event{Type: EventUpdated, Session: *s})
	}
}

// refreshInput derives the input channel from pane state, but leaves an InputAPI
// session (opencode) untouched: it prompts over its API whether or not a viewing
// pane is adopted, and never reverts to keystrokes. An agent's InputPolicy, when
// set, decides instead. Caller holds r.mu.
func (r *Registry) refreshInput(s *session.Session) {
	if p, ok := r.inputPolicies[s.Agent]; ok {
		s.Input = p(*s)
		return
	}
	if s.Input == session.InputAPI {
		return
	}
	if s.Tmux.PaneID != "" {
		s.Input = session.InputPane
	} else {
		s.Input = session.InputNone
	}
}

// setTranscriptPath points the session at a new transcript, invalidating the
// now-stale summary digest (/clear swaps to a fresh file). Caller holds r.mu.
func setTranscriptPath(s *session.Session, path string) {
	if path == "" || path == s.TranscriptPath {
		return
	}
	s.TranscriptPath = path
	s.Summary = nil
}

// applyStatusHint seeds a transcript-derived status onto a still-provisional session
// (StatusDiscovered/StatusStarting only). An idle hint also synthesizes an idle
// Interaction so clients show the compose. Caller holds r.mu.
func applyStatusHint(s *session.Session, hint session.Status) {
	if s.Status != session.StatusDiscovered && s.Status != session.StatusStarting {
		return
	}
	if hint == "" {
		if s.Tmux.PaneID == "" {
			return // no pane = can't type into it, leave discovered
		}
		if s.AgentSessionID == "" {
			setStatus(s, session.StatusStarting) // pane still at a startup gate, not ready
			return
		}
		hint = session.StatusIdle // has a session id but no transcript yet: ready for the first prompt
	}
	setStatus(s, hint)
	if hint == session.StatusIdle && s.Interaction == nil {
		s.Interaction = &session.Interaction{Kind: session.InteractionIdle}
	}
}

// SurfaceIdle surfaces the idle composer on a session after an interrupted turn.
// expectKind is a compare-and-swap against the interaction kind the caller
// observed: "" expects no pending prompt (a turn cleared to working by an
// interrupt), a prompt kind expects that stale prompt. If the current interaction
// no longer matches (a hook set a fresh prompt, or it already went idle), the swap
// is skipped so a genuine pending prompt is never clobbered. Status is Idle, not
// AwaitingInput, so no push fires. Returns whether anything changed.
func (r *Registry) SurfaceIdle(id string, expectKind session.InteractionKind) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.Status == session.StatusDead {
		return false
	}
	cur := session.InteractionKind("")
	if s.Interaction != nil {
		cur = s.Interaction.Kind
	}
	if cur == session.InteractionIdle || cur != expectKind {
		return false // already idle, or state changed since the caller observed it
	}
	setStatus(s, session.StatusIdle)
	s.Interaction = &session.Interaction{Kind: session.InteractionIdle}
	r.publish(Event{Type: EventUpdated, Session: *s})
	return true
}

// Caller holds r.mu.
func (r *Registry) markEnded(key string) {
	if key != "" {
		r.ended[key] = time.Now()
	}
}

func (r *Registry) clearEnded(key string) {
	if key != "" {
		delete(r.ended, key)
	}
}

func (r *Registry) recentlyEnded(key string) bool {
	if key == "" {
		return false
	}
	at, ok := r.ended[key]
	if !ok {
		return false
	}
	if time.Since(at) > r.endedGrace {
		delete(r.ended, key)
		return false
	}
	return true
}

// sweepEnded bounds the guard map: recentlyEnded only expires keys it is asked
// about, so a pane that ends and never reappears would leak. Caller holds r.mu.
func (r *Registry) sweepEnded() {
	for key, at := range r.ended {
		if time.Since(at) > r.endedGrace {
			delete(r.ended, key)
		}
	}
}

// remove deletes a session from all indices and publishes a removal event with the
// given final status. Caller holds r.mu.
func (r *Registry) remove(id, paneK string, finalStatus session.Status) {
	s := r.sessions[id]
	if s == nil {
		return
	}
	setStatus(s, finalStatus)
	removed := *s
	delete(r.sessions, id)
	delete(r.models, id)
	delete(r.subagents, id)
	delete(r.shells, id)
	r.index.clear(paneK, s.AgentSessionID)
	r.publish(Event{Type: EventRemoved, Session: removed})
}

// ClearInteraction drops a session's pending interaction and, if it was awaiting
// input, moves it back to working. Publishes an update so clients clear the alert.
func (r *Registry) ClearInteraction(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil {
		return
	}
	r.clearInteractionLocked(s)
}

// ClearPendingDecision drops a still-pending permission, question or plan
// interaction left by a decision nobody answered in argus. An idle interaction
// (a later event already moved the session on) is left as is.
func (r *Registry) ClearPendingDecision(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil || s.Interaction == nil || s.Interaction.Kind == session.InteractionIdle {
		return
	}
	r.clearInteractionLocked(s)
}

func (r *Registry) clearInteractionLocked(s *session.Session) {
	s.Interaction = nil
	if s.Status == session.StatusAwaitingInput {
		setStatus(s, session.StatusWorking)
	}
	r.publish(Event{Type: EventUpdated, Session: *s})
}

// ClearPane detaches an adopted tmux pane from an agent-keyed session (found by
// agent session id), reverting it to a paneless external session. Used when an
// on-demand terminal pane dies while the underlying session still exists. No-op
// when the session is unknown or already paneless.
func (r *Registry) ClearPane(agentSessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.index.findByAgentSession(agentSessionID)
	if !ok {
		return
	}
	s := r.sessions[id]
	if s == nil || s.Tmux.PaneID == "" {
		return
	}
	r.index.clear(PaneKey(s.Tmux.Server, s.Tmux.PaneID), "")
	s.Tmux = session.TmuxLocation{}
	s.Frontend = session.FrontendExternal
	r.refreshInput(s)
	r.publish(Event{Type: EventUpdated, Session: *s})
}

// RenameSession sets a session's name, publishing an update only when it
// changes. It touches nothing else, so a rename never clears a pending
// interaction or changes status. No-op for an unknown id.
func (r *Registry) RenameSession(id, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.Name == name {
		return
	}
	s.Name = name
	r.publish(Event{Type: EventUpdated, Session: *s})
}

// SetBranch updates a session's current git branch, publishing an update only
// when it changes. No-op for an unknown id. Called when a client opens a session
// (path-derived, so it catches a mid-session checkout).
func (r *Registry) SetBranch(id, branch string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil || s.Branch == branch {
		return
	}
	s.Branch = branch
	r.publish(Event{Type: EventUpdated, Session: *s})
}

func (r *Registry) SetWorkspaceID(id, workspaceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[id]
	if s == nil || s.WorkspaceID == workspaceID {
		return
	}
	s.WorkspaceID = workspaceID
	r.publish(Event{Type: EventUpdated, Session: *s})
}

// ModelUpdate is a live model and effort as an agent reports them.
type ModelUpdate struct {
	Name, Color string // formatted model name and its color; "" keeps the last
	// Effort is nil when the report says nothing about effort, keeping the
	// last one; "" means the model takes none.
	Effort *string
}

type liveModel struct{ name, color, effort string }

// setModel merges u into session id's live model. Caller holds r.mu.
func (r *Registry) setModel(id string, u ModelUpdate) {
	lm := r.models[id]
	if u.Name != "" {
		lm.name, lm.color = u.Name, u.Color
	}
	if u.Effort != nil {
		lm.effort = *u.Effort
	}
	r.models[id] = lm
}

// overlayModel lays the session's live model over its summary. It copies the
// summary: a published event shares the pointer. Caller holds r.mu.
func (r *Registry) overlayModel(s *session.Session) {
	lm, ok := r.models[s.ID]
	if !ok {
		return
	}
	var sum session.Summary
	if s.Summary != nil {
		sum = *s.Summary
	}
	if lm.name != "" {
		sum.ModelName, sum.ModelColor = lm.name, lm.color
	}
	sum.Effort = lm.effort
	s.Summary = &sum
}

// setSubagents keeps the Since of subagents already held. Caller holds r.mu.
func (r *Registry) setSubagents(id string, list []session.RunningSubagent, now time.Time) {
	r.subagents[id] = keepSince(r.subagents[id], list, func(sa *session.RunningSubagent) (string, *string) { return sa.ID, &sa.Since }, now)
}

// setShells keeps the Since of shells already held. Caller holds r.mu.
func (r *Registry) setShells(id string, list []session.RunningShell, now time.Time) {
	r.shells[id] = keepSince(r.shells[id], list, func(sh *session.RunningShell) (string, *string) { return sh.ID, &sh.Since }, now)
}

// keepSince returns list with each item's Since carried over from held by key,
// or stamped now when it is new.
func keepSince[T any](held, list []T, key func(*T) (string, *string), now time.Time) []T {
	was := make(map[string]string, len(held))
	for i := range held {
		id, since := key(&held[i])
		was[id] = *since
	}
	stamp := now.UTC().Format(time.RFC3339)
	next := slices.Clone(list)
	for i := range next {
		id, since := key(&next[i])
		*since = stamp
		if s, ok := was[id]; ok {
			*since = s
		}
	}
	return next
}

// overlayRunning copies so a published session never shares the stored
// slices. Caller holds r.mu.
func (r *Registry) overlayRunning(s *session.Session) {
	s.Subagents = cloneOrNil(r.subagents[s.ID])
	s.Shells = cloneOrNil(r.shells[s.ID])
}

func cloneOrNil[T any](list []T) []T {
	if len(list) == 0 {
		return nil
	}
	return slices.Clone(list)
}

// HookUpdate carries the correlation keys and fields from an agent hook event. Empty
// string fields leave an existing session unchanged. A non-empty Status sets the
// status; StatusDead removes the session.
type HookUpdate struct {
	Agent          string
	Server         session.TmuxServer
	PaneID         string // from $TMUX_PANE; primary correlation key
	AgentSessionID string
	// PrevAgentSessionID is the agent session the process just left; its record
	// follows the process to AgentSessionID.
	PrevAgentSessionID string
	Name               string
	Cwd                string
	Repo               string // git repo basename for Cwd, when known
	TranscriptPath     string
	// Frontend classifies the session's UI host. Never downgrades a pane-bearing
	// session (see ApplyHook).
	Frontend session.Frontend
	// InputAPI (agents that prompt over their own API) is sticky: once set it stays
	// and never reverts to a pane channel. Pane sessions get InputPane from
	// refreshInput, not from the hook.
	Input  session.InputMode
	Status session.Status
	// Summary is a refreshed transcript digest, or nil to keep the cached one.
	Summary *session.Summary
	// Interaction is the pending user request, applied when Status is set: non-nil
	// records it, nil clears any prior one.
	Interaction *session.Interaction
	// ReplaceInteraction bypasses mergeInteraction and sets Interaction directly.
	// Used by the Stop hook: the turn ended, so the idle prompt must supersede any
	// stale pending interaction.
	ReplaceInteraction bool
	Model              *ModelUpdate
	// Subagents and Shells are nil to leave the running set unchanged; non-nil
	// replaces it (empty clears).
	Subagents *[]session.RunningSubagent
	Shells    *[]session.RunningShell
}

// ApplyHook correlates a hook event to a session (by pane, else agent session id,
// else previous agent session id), creating one if none matches. Returns the
// session and whether it still exists.
func (r *Registry) ApplyHook(u HookUpdate) (session.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pKey := ""
	if u.PaneID != "" {
		pKey = PaneKey(u.Server, u.PaneID)
	}

	var s *session.Session
	if pKey != "" {
		if id, ok := r.index.findByPane(pKey); ok {
			s = r.sessions[id]
		}
	}
	if s == nil && u.AgentSessionID != "" {
		if id, ok := r.index.findByAgentSession(u.AgentSessionID); ok {
			s = r.sessions[id]
		}
	}
	if s == nil && u.AgentSessionID != "" && u.PrevAgentSessionID != "" {
		if id, ok := r.index.findByAgentSession(u.PrevAgentSessionID); ok {
			s = r.sessions[id]
		}
	}
	// The record now shows another session: the old one's name and transcript
	// would otherwise stay, since empty fields below keep what is there.
	if s != nil && u.AgentSessionID != "" && u.PrevAgentSessionID != "" && s.AgentSessionID == u.PrevAgentSessionID {
		s.Name, s.TranscriptPath, s.Summary = "", "", nil
	}

	created := false
	if s == nil {
		// Prefer a pane-based ID so a later discovery scan correlates to this record.
		id := pKey
		if id == "" {
			id = AgentSessionKey(u.Agent, u.AgentSessionID)
		}
		// A headless agent's pane is a disposable viewer, so key on its stable
		// AgentSessionID instead: a pane-based ID would change across adopt/respawn
		// and split session identity (notification dedup, cross-scan correlation).
		if u.Input == session.InputAPI && u.AgentSessionID != "" {
			id = AgentSessionKey(u.Agent, u.AgentSessionID)
		}
		id = r.freeID(id)
		s = &session.Session{ID: id, Agent: u.Agent, Source: session.SourceHooked}
		if u.PaneID != "" {
			s.Tmux.Server = u.Server
			s.Tmux.PaneID = u.PaneID
		}
		r.sessions[id] = s
		setStatus(s, session.StatusIdle)
		if pKey != "" {
			r.index.setPane(pKey, id)
		}
		created = true
	} else if pKey != "" && s.Tmux.PaneID == "" {
		// Adopt an on-demand pane onto an existing paneless record (an OpenCode
		// session spawned into a tmux pane for terminal control). The record keeps
		// its agent-keyed ID; the pane key is added as an alias.
		s.Tmux.Server = u.Server
		s.Tmux.PaneID = u.PaneID
		r.index.setPane(pKey, s.ID)
	}

	// A running-set snapshot repeats on every turn end: publish it only when it
	// changed the session.
	var before *session.Session
	if !created && (u.Subagents != nil || u.Shells != nil) {
		b := *s
		before = &b
	}

	r.reindexAgentSession(s, u.AgentSessionID)
	if u.Name != "" {
		s.Name = u.Name
	}
	if u.Cwd != "" {
		s.Cwd = u.Cwd
	}
	if u.Repo != "" {
		s.Repo = u.Repo
	}
	setTranscriptPath(s, u.TranscriptPath)
	// Never downgrade a pane-bearing session: a pane means tmux, whatever a later
	// correlated hook claims.
	if s.Tmux.PaneID != "" {
		s.Frontend = session.FrontendTmux
	} else if u.Frontend != "" {
		s.Frontend = u.Frontend
	}
	if u.Input == session.InputAPI {
		s.Input = session.InputAPI
	}
	r.refreshInput(s)
	// Non-nil replaces the cached summary; nil keeps it.
	if u.Summary != nil {
		s.Summary = u.Summary
	}

	if u.Model != nil {
		r.setModel(s.ID, *u.Model)
	}
	r.overlayModel(s)
	if u.Subagents != nil {
		r.setSubagents(s.ID, *u.Subagents, time.Now())
	}
	if u.Shells != nil {
		r.setShells(s.ID, *u.Shells, time.Now())
	}
	r.overlayRunning(s)

	if u.Status == session.StatusDead {
		r.markEnded(pKey)
		r.markEnded(s.AgentSessionID)
		r.remove(s.ID, pKey, session.StatusDead)
		dead := *s
		dead.Status = session.StatusDead
		return dead, false
	}
	// Any non-dead hook proves the agent is live, so discovery is free again.
	r.clearEnded(pKey)
	r.clearEnded(u.AgentSessionID)
	if u.Status != "" {
		setStatus(s, u.Status)
		// A status decision (re)sets the pending interaction, but a bare Notification
		// must not clobber a richer one (see mergeInteraction). Stop opts out via
		// ReplaceInteraction.
		if u.ReplaceInteraction {
			s.Interaction = u.Interaction
		} else {
			s.Interaction = mergeInteraction(s.Interaction, u.Interaction)
		}
	}
	if before != nil && reflect.DeepEqual(*before, *s) {
		return *s, true
	}
	evType := EventUpdated
	if created {
		evType = EventAdded
	}
	r.publish(Event{Type: evType, Session: *s})
	return *s, true
}

// freeID returns id, or id with a counter suffix when a record already holds
// it: a record keeps its id across a session switch, so the agent session it
// was named after can come back under a new record. Caller holds r.mu.
func (r *Registry) freeID(id string) string {
	if _, taken := r.sessions[id]; !taken {
		return id
	}
	for n := 2; ; n++ {
		next := id + "#" + strconv.Itoa(n)
		if _, taken := r.sessions[next]; !taken {
			return next
		}
	}
}

// mergeInteraction stops a low-information update (idle Notification, bare
// permission) from clobbering a richer pending interaction (ToolInput, questions,
// plan): it keeps the existing content but adopts the newer message. A content-bearing
// update still replaces, and nil still clears. Stop bypasses this via ReplaceInteraction.
func mergeInteraction(old, next *session.Interaction) *session.Interaction {
	if next == nil || old == nil {
		return next
	}
	if !hasContent(next) && hasContent(old) {
		merged := *old
		if next.Message != "" {
			merged.Message = next.Message
		}
		return &merged
	}
	return next
}

// hasContent reports whether an interaction carries detail (tool input, questions,
// plan) that a bare Notification should not overwrite.
func hasContent(ix *session.Interaction) bool {
	return ix.ToolInput != "" || len(ix.Questions) > 0 || ix.Plan != ""
}
