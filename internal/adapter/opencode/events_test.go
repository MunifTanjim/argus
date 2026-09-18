package opencode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func seedSession(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	reg.ReconcileSessions(Agent, []registry.DiscoveredSession{{
		AgentSessionID: id, Cwd: "/repo", TranscriptPath: id, Frontend: session.FrontendExternal,
	}})
}

func sessionKeyFor(reg *registry.Registry, agentSessionID string) string {
	for _, s := range reg.Snapshot() {
		if s.Agent == Agent && s.AgentSessionID == agentSessionID {
			return s.ID
		}
	}
	return ""
}

func statusOf(t *testing.T, reg *registry.Registry, agentSessionID string) session.Status {
	t.Helper()
	key := sessionKeyFor(reg, agentSessionID)
	if key == "" {
		t.Fatalf("no session with AgentSessionID %q", agentSessionID)
	}
	s, ok := reg.Get(key)
	if !ok {
		t.Fatalf("session %q not found", key)
	}
	return s.Status
}

func TestApplyEventStatusMap(t *testing.T) {
	reg := registry.New()
	d := &discoverer{reg: reg, pendPerm: map[string]string{}}
	seedSession(t, reg, "ses_1")

	d.applyEvent(globalEvent{Payload: eventPayload{
		Type: "session.idle", Properties: json.RawMessage(`{"sessionID":"ses_1"}`),
	}})
	if got := statusOf(t, reg, "ses_1"); got != session.StatusAwaitingInput {
		t.Fatalf("after idle, status = %q", got)
	}

	d.applyEvent(globalEvent{Payload: eventPayload{
		Type:       "permission.updated",
		Properties: json.RawMessage(`{"id":"perm_9","sessionID":"ses_1","type":"bash","messageID":"msg_1","title":"bash","metadata":{},"time":{"created":1}}`),
	}})
	if got := statusOf(t, reg, "ses_1"); got != session.StatusAwaitingInput {
		t.Fatalf("after permission, status = %q", got)
	}
	if s, _ := reg.Get(sessionKeyFor(reg, "ses_1")); s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("expected permission interaction, got %+v", s.Interaction)
	}
	d.mu.Lock()
	pid := d.pendPerm["ses_1"]
	d.mu.Unlock()
	if pid != "perm_9" {
		t.Fatalf("pendPerm = %q", pid)
	}
}

func TestParseSSE(t *testing.T) {
	stream := "data: {\"directory\":\"/repo\",\"payload\":{\"type\":\"session.idle\",\"properties\":{\"sessionID\":\"ses_1\"}}}\n\n"
	var got []globalEvent
	if err := parseSSE(strings.NewReader(stream), func(e globalEvent) { got = append(got, e) }); err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 1 || got[0].Payload.Type != "session.idle" {
		t.Fatalf("parsed = %+v", got)
	}
}
