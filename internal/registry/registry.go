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

	// Replay marks an event that re-states existing session state rather than
	// reporting a fresh change — i.e. a snapshot the aggregator emits when a
	// source connects or reconnects. It is a gateway-internal hint (never sent
	// on the wire) so consumers like the push watcher can record the state
	// without mistaking it for a live transition and re-notifying.
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

// paneKey uniquely identifies a pane across servers (pane ids are unique per
// server, so the server must be part of the key).
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

// Subscribe returns a channel of events plus a cancel func. The channel is
// buffered; if a subscriber falls behind, events are dropped for that
// subscriber rather than blocking the registry.
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

// DiscoveredPane is what an adapter reports for a pane it believes runs a
// supported tool.
type DiscoveredPane struct {
	Tool        string
	Server      session.TmuxServer
	PaneID      string
	SessionName string
	WindowIndex int
	CurrentPath string
	Repo        string // git repo basename for CurrentPath, when known

	// Claude-side enrichment derived at discovery from ~/.claude/sessions/<pid>.json
	// (all optional; empty/nil when the process file isn't available yet).
	ClaudeSessionID string
	Name            string // Claude's own session name
	Cwd             string
	TranscriptPath  string
	Summary         *session.Summary // computed from the transcript; nil when unavailable

	// StatusHint is the transcript-derived live status at discovery
	// (session.StatusWorking / StatusIdle), or "" when unknown. Seeds a new or
	// still-discovered session; never overrides a hook-derived status.
	StatusHint session.Status
}

// ReconcileDiscovered syncs the registry's discovered sessions for a (tool,
// server) pair to match the provided panes: adds new, updates existing, and
// marks vanished panes dead. Hook-promoted sessions keep their richer status.
func (r *Registry) ReconcileDiscovered(tool string, server session.TmuxServer, panes []DiscoveredPane) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[string]bool, len(panes))
	for _, p := range panes {
		key := paneKey(server, p.PaneID)
		seen[key] = true
		if id, ok := r.index.findByPane(key); ok {
			s := r.sessions[id]
			// Refresh mutable tmux fields; never downgrade a hook-derived status.
			s.Tmux.SessionName = p.SessionName
			s.Tmux.WindowIndex = p.WindowIndex
			s.Tmux.CurrentPath = p.CurrentPath
			if p.Repo != "" {
				s.Repo = p.Repo
			}
			r.enrichDiscovered(s, p)
			applyStatusHint(s, p.StatusHint)
			r.publish(Event{Type: EventUpdated, Session: *s})
			continue
		}
		s := &session.Session{
			ID:     key,
			Tool:   tool,
			Status: session.StatusDiscovered,
			Source: session.SourceDiscovered,
			Repo:   p.Repo,
			Tmux: session.TmuxLocation{
				Server:      server,
				PaneID:      p.PaneID,
				SessionName: p.SessionName,
				WindowIndex: p.WindowIndex,
				CurrentPath: p.CurrentPath,
			},
		}
		r.enrichDiscovered(s, p)
		applyStatusHint(s, p.StatusHint)
		r.sessions[s.ID] = s
		r.index.setPane(key, s.ID)
		r.publish(Event{Type: EventAdded, Session: *s})
	}

	// Remove sessions on this (tool, server) whose pane is gone.
	for key, id := range r.index.byPane {
		s := r.sessions[id]
		if s.Tool != tool || s.Tmux.Server != server {
			continue
		}
		if seen[key] {
			continue
		}
		r.remove(id, key, session.StatusDead)
	}
}

// enrichDiscovered applies Claude-side fields from ~/.claude/sessions/<pid>.json
// onto a session (identity, summary) and registers the byClaude index for later
// hook correlation. A nil summary never downgrades an existing one. Does not
// promote status past StatusDiscovered. Caller holds r.mu.
func (r *Registry) enrichDiscovered(s *session.Session, p DiscoveredPane) {
	if p.ClaudeSessionID != "" && s.ClaudeSessionID != p.ClaudeSessionID {
		s.ClaudeSessionID = p.ClaudeSessionID
		r.index.setClaude(p.ClaudeSessionID, s.ID)
	}
	if p.Name != "" {
		s.Name = p.Name
	}
	if p.Cwd != "" {
		s.Cwd = p.Cwd
	}
	if p.TranscriptPath != "" {
		s.TranscriptPath = p.TranscriptPath
	}
	if p.Summary != nil {
		s.Summary = p.Summary
	}
}

// applyStatusHint seeds a transcript-derived status onto a session that has no
// authoritative (hook-derived) status yet — i.e. it is still StatusDiscovered.
// An idle hint also synthesizes an idle Interaction so clients show the compose.
// Caller holds r.mu.
func applyStatusHint(s *session.Session, hint session.Status) {
	if hint == "" || s.Status != session.StatusDiscovered {
		return
	}
	s.Status = hint
	if hint == session.StatusIdle && s.Interaction == nil {
		s.Interaction = &session.Interaction{Kind: session.InteractionIdle}
	}
}

// remove deletes a session from all indexes and publishes a removal event with
// the given final status. Caller must hold r.mu.
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

// ClearInteraction drops a session's pending interaction (e.g. after the user
// answered a parked PermissionRequest) and, if it was awaiting input, moves it
// back to working. Publishes an update so clients clear the alert promptly.
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

// HookUpdate carries the correlation keys and fields derived from a tool hook
// event. Empty string fields are left unchanged on an existing session. A
// non-empty Status sets the session status; StatusDead removes the session.
type HookUpdate struct {
	Tool            string
	Server          session.TmuxServer
	PaneID          string // from $TMUX_PANE; primary correlation key
	ClaudeSessionID string
	Cwd             string
	Repo            string // git repo basename for Cwd, when known
	TranscriptPath  string
	Status          session.Status
	// Summary is a refreshed transcript digest, or nil to keep the cached one.
	Summary *session.Summary
	// Interaction is the pending user request. Applied when Status is set:
	// non-nil records the request, nil clears any prior one.
	Interaction *session.Interaction
	// ReplaceInteraction bypasses mergeInteraction and sets Interaction directly.
	// Used by the Stop hook: the turn has ended, so any prior interaction is stale
	// and the idle "waiting for input" one must supersede it.
	ReplaceInteraction bool
}

// ApplyHook correlates a hook event to a session (by pane, else by claude
// session id) and enriches it, creating a hooked session if none matches.
// Returns the resulting session and whether it still exists (false if removed).
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
		// Create a session learned via hook. Prefer a pane-based ID so a later
		// discovery scan correlates to the same record.
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
	// Non-nil replaces the cached summary; nil keeps the prior digest.
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
		// A status decision also (re)sets the pending interaction — but a bare
		// Notification must not clobber a richer one (see mergeInteraction).
		// Stop opts out via ReplaceInteraction since its turn-end idle supersedes
		// whatever was pending.
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

// mergeInteraction prevents a low-information update (idle Notification, bare
// permission with no ToolInput) from clobbering a richer pending interaction
// (e.g. PermissionRequest's ToolInput, AskUserQuestion, ExitPlanMode). It keeps
// the existing content and adopts only the newer message. A content-bearing update
// still replaces, and nil still clears. Stop bypasses this via
// HookUpdate.ReplaceInteraction.
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
