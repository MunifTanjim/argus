package registry

// sessionIndex maps tmux panes and agent session ids to internal session ids, so a
// scan or hook can correlate to an existing session. Owns the invariant: a session
// is keyed by whichever ids it has, and cleared from both indices on removal.
type sessionIndex struct {
	byPane         map[string]string // paneKey -> internal session id
	byAgentSession map[string]string // agent session id -> internal session id
}

func newSessionIndex() *sessionIndex {
	return &sessionIndex{
		byPane:         make(map[string]string),
		byAgentSession: make(map[string]string),
	}
}

func (x *sessionIndex) findByPane(key string) (string, bool) {
	id, ok := x.byPane[key]
	return id, ok
}

func (x *sessionIndex) findByAgentSession(agentSessionID string) (string, bool) {
	id, ok := x.byAgentSession[agentSessionID]
	return id, ok
}

func (x *sessionIndex) setPane(key, id string) { x.byPane[key] = id }

func (x *sessionIndex) setAgentSession(agentSessionID, id string) {
	x.byAgentSession[agentSessionID] = id
}

// clear drops a session's entries from both indices (empty keys ignored).
func (x *sessionIndex) clear(paneKey, agentSessionID string) {
	if paneKey != "" {
		delete(x.byPane, paneKey)
	}
	if agentSessionID != "" {
		delete(x.byAgentSession, agentSessionID)
	}
}
