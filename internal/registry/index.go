package registry

// sessionIndex maps tmux panes and Claude session ids to internal session ids so a
// discovery scan or a hook can correlate to an existing session. It owns the
// consistency rule for the two indices: a session is registered under whichever keys
// it has, and cleared from both when it is removed.
type sessionIndex struct {
	byPane   map[string]string // paneKey -> internal session id
	byClaude map[string]string // claude session id -> internal session id
}

func newSessionIndex() *sessionIndex {
	return &sessionIndex{
		byPane:   make(map[string]string),
		byClaude: make(map[string]string),
	}
}

// findByPane returns the session id registered for a pane key, if any.
func (x *sessionIndex) findByPane(key string) (string, bool) {
	id, ok := x.byPane[key]
	return id, ok
}

// findByClaude returns the session id registered for a Claude session id, if any.
func (x *sessionIndex) findByClaude(claudeID string) (string, bool) {
	id, ok := x.byClaude[claudeID]
	return id, ok
}

// setPane points a pane key at a session id.
func (x *sessionIndex) setPane(key, id string) { x.byPane[key] = id }

// setClaude points a Claude session id at a session id.
func (x *sessionIndex) setClaude(claudeID, id string) { x.byClaude[claudeID] = id }

// clear drops a session's entries from both indices (empty keys are ignored), so the
// "removed from one ⇒ removed from both" invariant lives in one place.
func (x *sessionIndex) clear(paneKey, claudeID string) {
	if paneKey != "" {
		delete(x.byPane, paneKey)
	}
	if claudeID != "" {
		delete(x.byClaude, claudeID)
	}
}
