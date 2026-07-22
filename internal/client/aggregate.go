package client

import (
	"encoding/json"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// sessionAddressed methods carry a composite session_id the client splits to a
// node-local id and routes to that node.
var sessionAddressed = map[string]bool{
	api.MethodSessionTranscriptView: true,
	api.MethodSessionToolDetail:     true,
	api.MethodSessionCapture:        true,
	api.MethodSessionInput:          true,
	api.MethodSessionKey:            true,
	api.MethodSessionRespond:        true,
	api.MethodSessionKill:           true,
	api.MethodSessionChangedFiles:   true,
	api.MethodSessionFileDiff:       true,
	api.MethodSessionCommits:        true,
	api.MethodSessionCommitFiles:    true,
	api.MethodSessionFocus:          true,
	api.MethodTranscriptSubscribe:   true,
	api.MethodTerminalOpen:          true,
}

// nodeAddressed methods route by an explicit node_id (or the sole node).
var nodeAddressed = map[string]bool{
	api.MethodSessionSpawn:              true,
	api.MethodSessionResume:             true,
	api.MethodAgentsList:                true,
	api.MethodSessionExport:             true,
	api.MethodSessionsHistorySessions:   true,
	api.MethodSessionsHistoryTranscript: true,
	api.MethodSessionHistoryToolDetail:  true,
}

// terminalHandleAddressed methods carry a term_id (not a session_id); the client
// routes them to the node the terminal was opened on.
var terminalHandleAddressed = map[string]bool{
	api.MethodTerminalInput:  true,
	api.MethodTerminalResize: true,
	api.MethodTerminalClose:  true,
}

// pushFanoutMethods are sent to every connected node (each holds its own device
// store) rather than routed to one. push.vapidKey stays a gateway passthrough.
var pushFanoutMethods = map[string]bool{
	api.MethodPushRegister:   true,
	api.MethodPushUnregister: true,
	api.MethodPushTest:       true,
}

// compositeResultMethods return a node-local session_id in their result that must
// be composited so the client can address it afterward.
var compositeResultMethods = map[string]bool{
	api.MethodSessionSpawn:  true,
	api.MethodSessionResume: true,
}

// withOrigin stamps a session with its node origin + composite id and clears
// Offline (it is currently reported).
func withOrigin(s session.Session, nodeID, label string) session.Session {
	s.ID = session.CompositeID(nodeID, s.ID)
	s.NodeID = nodeID
	s.NodeLabel = label
	s.Offline = false
	return s
}

// rewriteSessionID replaces only the session_id field, preserving other fields.
func rewriteSessionID(params json.RawMessage, id string) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return nil, err
		}
	}
	idRaw, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	m["session_id"] = idRaw
	return json.Marshal(m)
}

func stringField(params json.RawMessage, field string) (string, error) {
	m := map[string]json.RawMessage{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &m); err != nil {
			return "", err
		}
	}
	if raw, ok := m[field]; ok {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return "", nil
}

func sessionIDFromParams(p json.RawMessage) (string, error) { return stringField(p, "session_id") }
func nodeIDFromParams(p json.RawMessage) (string, error)    { return stringField(p, "node_id") }
func subIDFromParams(p json.RawMessage) (string, error)     { return stringField(p, "sub_id") }
func termIDFromParams(p json.RawMessage) (string, error)    { return stringField(p, "term_id") }

// stampEvent rewrites a session.event's node-local session with composite origin.
// On any decode error it returns params unchanged (best-effort).
func stampEvent(params json.RawMessage, nodeID, label string) json.RawMessage {
	var ev registry.Event
	if json.Unmarshal(params, &ev) != nil {
		return params
	}
	ev.Session = withOrigin(ev.Session, nodeID, label)
	b, err := json.Marshal(ev)
	if err != nil {
		return params
	}
	return b
}
