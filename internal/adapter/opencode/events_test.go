package opencode

import (
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

	d.applyEvent(sseFrame{
		Type: "session.idle",
		Data: []byte(`{"sessionID":"ses_1"}`),
	})
	if got := statusOf(t, reg, "ses_1"); got != session.StatusAwaitingInput {
		t.Fatalf("after idle, status = %q", got)
	}

	d.applyEvent(sseFrame{
		Type: "permission.updated",
		Data: []byte(`{"id":"req_1","sessionID":"ses_1","action":"bash","resources":[]}`),
	})
	if got := statusOf(t, reg, "ses_1"); got != session.StatusAwaitingInput {
		t.Fatalf("after permission, status = %q", got)
	}
	if s, _ := reg.Get(sessionKeyFor(reg, "ses_1")); s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("expected permission interaction, got %+v", s.Interaction)
	}
	d.mu.Lock()
	pid := d.pendPerm["ses_1"]
	d.mu.Unlock()
	if pid != "req_1" {
		t.Fatalf("pendPerm = %q, want req_1", pid)
	}
}

func TestParseSSE(t *testing.T) {
	stream := ": heartbeat\n" +
		"\n" +
		`data: {"id":"evt_1","type":"session.idle","data":{"sessionID":"ses_1"}}` + "\n" +
		"\n" +
		`data: {"id":"evt_2","type":"permission.updated","data":{"id":"req_1","sessionID":"ses_1","action":"bash"}}` + "\n"

	var got []sseFrame
	if err := parseSSE(strings.NewReader(stream), func(f sseFrame) { got = append(got, f) }); err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 frames, got %d: %+v", len(got), got)
	}
	if got[0].Type != "session.idle" {
		t.Fatalf("frame[0].Type = %q, want session.idle", got[0].Type)
	}
	if got[1].Type != "permission.updated" {
		t.Fatalf("frame[1].Type = %q, want permission.updated", got[1].Type)
	}
}

func TestParseSSEAndApply(t *testing.T) {
	reg := registry.New()
	d := &discoverer{reg: reg, pendPerm: map[string]string{}}
	seedSession(t, reg, "ses_1")

	stream := ": heartbeat\n" +
		"\n" +
		`data: {"id":"evt_1","type":"session.idle","data":{"sessionID":"ses_1"}}` + "\n" +
		"\n" +
		`data: {"id":"evt_2","type":"permission.updated","data":{"id":"req_1","sessionID":"ses_1","action":"bash"}}` + "\n"

	if err := parseSSE(strings.NewReader(stream), d.applyEvent); err != nil {
		t.Fatalf("parseSSE: %v", err)
	}

	if got := statusOf(t, reg, "ses_1"); got != session.StatusAwaitingInput {
		t.Fatalf("status = %q, want StatusAwaitingInput", got)
	}
	s, _ := reg.Get(sessionKeyFor(reg, "ses_1"))
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("expected permission interaction, got %+v", s.Interaction)
	}
	d.mu.Lock()
	pid := d.pendPerm["ses_1"]
	d.mu.Unlock()
	if pid != "req_1" {
		t.Fatalf("pendPerm[ses_1] = %q, want req_1", pid)
	}
}
