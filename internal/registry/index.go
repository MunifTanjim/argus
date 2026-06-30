package registry

// sessionIndex maps tmux panes and Claude session ids to internal session ids, so a
// scan or hook can correlate to an existing session. Owns the invariant: a session
// is keyed by whichever ids it has, and cleared from both indices on removal.
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

func (x *sessionIndex) findByPane(key string) (string, bool) {
	id, ok := x.byPane[key]
	return id, ok
}

func (x *sessionIndex) findByClaude(claudeID string) (string, bool) {
	id, ok := x.byClaude[claudeID]
	return id, ok
}

func (x *sessionIndex) setPane(key, id string) { x.byPane[key] = id }

func (x *sessionIndex) setClaude(claudeID, id string) { x.byClaude[claudeID] = id }

// clear drops a session's entries from both indices (empty keys ignored).
func (x *sessionIndex) clear(paneKey, claudeID string) {
	if paneKey != "" {
		delete(x.byPane, paneKey)
	}
	if claudeID != "" {
		delete(x.byClaude, claudeID)
	}
}
