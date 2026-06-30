// Package registry holds argus's live model of all known sessions and publishes
// change events to subscribers (clients, the TUI). It is tool- and
// transport-agnostic: adapters feed it discoveries and hook events; the API
// layer reads snapshots and streams events.
package registry

import (
	"sync"

	"github.com/MunifTanjim/argus/internal/session"
)

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
	index    *sessionIndex               // pane/claude-id correlation (see index.go)
	subs     map[int]chan Event
	nextSub  int
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		sessions: make(map[string]*session.Session),
		index:    newSessionIndex(),
		subs:     make(map[int]chan Event),
	}
}

// paneKey identifies a pane across servers; pane ids are only unique per server,
// so the server must be part of the key.
func paneKey(server session.TmuxServer, paneID string) string {
	return string(server) + ":" + paneID
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

// DiscoveredSession is one reconcile input: a Claude session from a scan, optionally
// pinned to a tmux pane. Correlated by pane key when HasPane, else by claude id.
type DiscoveredSession struct {
	ClaudeSessionID string
	HasPane         bool
	Server          session.TmuxServer
	PaneID          string
	SessionName     string
	WindowIndex     int
	CurrentPath     string
	Frontend        session.Frontend // adapter-computed (tmux/vscode/external)
	Name            string
	Cwd             string
	Repo            string
	TranscriptPath  string
	Summary         *session.Summary
	StatusHint      session.Status
}

// ReconcileSessions syncs a tool's sessions to the scan's live set: add new,
// refresh existing (correlating pane-first, else claude id), prune any whose pane
// and claude id were both absent. The dual-or liveness rule keeps a session alive
// through a transient pane-correlation miss (via claude id) or vice versa.
func (r *Registry) ReconcileSessions(tool string, found []DiscoveredSession) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seenPane := map[string]bool{}
	seenClaude := map[string]bool{}

	for _, f := range found {
		paneK := ""
		if f.HasPane {
			paneK = paneKey(f.Server, f.PaneID)
			seenPane[paneK] = true
		}
		if f.ClaudeSessionID != "" {
			seenClaude[f.ClaudeSessionID] = true
		}

		var s *session.Session
		if paneK != "" {
			if id, ok := r.index.findByPane(paneK); ok {
				s = r.sessions[id]
			}
		}
		if s == nil && f.ClaudeSessionID != "" {
			if id, ok := r.index.findByClaude(f.ClaudeSessionID); ok {
				s = r.sessions[id]
			}
		}

		created := false
		if s == nil {
			id := paneK
			if id == "" {
				id = "claude:" + f.ClaudeSessionID
			}
			s = &session.Session{
				ID:     id,
				Tool:   tool,
				Status: session.StatusDiscovered,
				Source: session.SourceDiscovered,
			}
			r.sessions[id] = s
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
		if f.ClaudeSessionID != "" && s.ClaudeSessionID != f.ClaudeSessionID {
			s.ClaudeSessionID = f.ClaudeSessionID
			r.index.setClaude(f.ClaudeSessionID, s.ID)
		}

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
		if f.Cwd != "" {
			s.Cwd = f.Cwd
		}
		if f.Repo != "" {
			s.Repo = f.Repo
		}
		if f.TranscriptPath != "" {
			s.TranscriptPath = f.TranscriptPath
		}
		if f.Summary != nil {
			s.Summary = f.Summary
		}
		applyStatusHint(s, f.StatusHint)

		evType := EventUpdated
		if created {
			evType = EventAdded
		}
		r.publish(Event{Type: evType, Session: *s})
	}

	for id, s := range r.sessions {
		if s.Tool != tool {
			continue
		}
		pk := ""
		alive := false
		if s.Tmux.PaneID != "" {
			pk = paneKey(s.Tmux.Server, s.Tmux.PaneID)
			if seenPane[pk] {
				alive = true
			}
		}
		if s.ClaudeSessionID != "" && seenClaude[s.ClaudeSessionID] {
			alive = true
		}
		if !alive {
			r.remove(id, pk, session.StatusDead)
		}
	}
}

// applyStatusHint seeds a transcript-derived status onto a still-StatusDiscovered
// session (no authoritative hook status yet). An idle hint also synthesizes an idle
// Interaction so clients show the compose. Caller holds r.mu.
func applyStatusHint(s *session.Session, hint session.Status) {
	if hint == "" || s.Status != session.StatusDiscovered {
		return
	}
	s.Status = hint
	if hint == session.StatusIdle && s.Interaction == nil {
		s.Interaction = &session.Interaction{Kind: session.InteractionIdle}
	}
}

// remove deletes a session from all indices and publishes a removal event with the
// given final status. Caller holds r.mu.
func (r *Registry) remove(id, paneK string, finalStatus session.Status) {
	s := r.sessions[id]
	if s == nil {
		return
	}
	s.Status = finalStatus
	removed := *s
	delete(r.sessions, id)
	r.index.clear(paneK, s.ClaudeSessionID)
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
	s.Interaction = nil
	if s.Status == session.StatusAwaitingInput {
		s.Status = session.StatusWorking
	}
	r.publish(Event{Type: EventUpdated, Session: *s})
}

// HookUpdate carries the correlation keys and fields from a tool hook event. Empty
// string fields leave an existing session unchanged. A non-empty Status sets the
// status; StatusDead removes the session.
type HookUpdate struct {
	Tool            string
	Server          session.TmuxServer
	PaneID          string // from $TMUX_PANE; primary correlation key
	ClaudeSessionID string
	Cwd             string
	Repo            string // git repo basename for Cwd, when known
	TranscriptPath  string
	// Frontend classifies the session's UI host. Never downgrades a pane-bearing
	// session (see ApplyHook).
	Frontend session.Frontend
	Status   session.Status
	// Summary is a refreshed transcript digest, or nil to keep the cached one.
	Summary *session.Summary
	// Interaction is the pending user request, applied when Status is set: non-nil
	// records it, nil clears any prior one.
	Interaction *session.Interaction
	// ReplaceInteraction bypasses mergeInteraction and sets Interaction directly.
	// Used by the Stop hook: the turn ended, so the idle prompt must supersede any
	// stale pending interaction.
	ReplaceInteraction bool
}

// ApplyHook correlates a hook event to a session (by pane, else claude id) and
// enriches it, creating a hooked session if none matches. Returns the session and
// whether it still exists (false if removed).
func (r *Registry) ApplyHook(u HookUpdate) (session.Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pKey := ""
	if u.PaneID != "" {
		pKey = paneKey(u.Server, u.PaneID)
	}

	var s *session.Session
	if pKey != "" {
		if id, ok := r.index.findByPane(pKey); ok {
			s = r.sessions[id]
		}
	}
	if s == nil && u.ClaudeSessionID != "" {
		if id, ok := r.index.findByClaude(u.ClaudeSessionID); ok {
			s = r.sessions[id]
		}
	}

	created := false
	if s == nil {
		// Prefer a pane-based ID so a later discovery scan correlates to this record.
		id := pKey
		if id == "" {
			id = "claude:" + u.ClaudeSessionID
		}
		s = &session.Session{ID: id, Tool: u.Tool, Status: session.StatusIdle, Source: session.SourceHooked}
		if u.PaneID != "" {
			s.Tmux.Server = u.Server
			s.Tmux.PaneID = u.PaneID
		}
		r.sessions[id] = s
		if pKey != "" {
			r.index.setPane(pKey, id)
		}
		created = true
	}

	if u.ClaudeSessionID != "" && s.ClaudeSessionID != u.ClaudeSessionID {
		s.ClaudeSessionID = u.ClaudeSessionID
		r.index.setClaude(u.ClaudeSessionID, s.ID)
	}
	if u.Cwd != "" {
		s.Cwd = u.Cwd
	}
	if u.Repo != "" {
		s.Repo = u.Repo
	}
	if u.TranscriptPath != "" {
		s.TranscriptPath = u.TranscriptPath
	}
	// Never downgrade a pane-bearing session: a pane means tmux, whatever a later
	// correlated hook claims.
	if s.Tmux.PaneID != "" {
		s.Frontend = session.FrontendTmux
	} else if u.Frontend != "" {
		s.Frontend = u.Frontend
	}
	// Non-nil replaces the cached summary; nil keeps it.
	if u.Summary != nil {
		s.Summary = u.Summary
	}

	if u.Status == session.StatusDead {
		r.remove(s.ID, pKey, session.StatusDead)
		dead := *s
		dead.Status = session.StatusDead
		return dead, false
	}
	if u.Status != "" {
		s.Status = u.Status
		// A status decision (re)sets the pending interaction, but a bare Notification
		// must not clobber a richer one (see mergeInteraction). Stop opts out via
		// ReplaceInteraction.
		if u.ReplaceInteraction {
			s.Interaction = u.Interaction
		} else {
			s.Interaction = mergeInteraction(s.Interaction, u.Interaction)
		}
	}
	evType := EventUpdated
	if created {
		evType = EventAdded
	}
	r.publish(Event{Type: evType, Session: *s})
	return *s, true
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
