//go:build pi_live

package pi

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Run with: go test ./internal/adapter/pi/ -tags pi_live -run TestLiveSmoke -v
// Requires the pi binary on PATH (no model auth needed: spawn with an empty
// prompt, then drive bash + dialogs only).
func TestLiveSmoke(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, cmd, err := spawnRPC(ctx, t.TempDir(), "smoke-"+newSessionID(), "", "")
	if err != nil {
		t.Fatalf("spawnRPC: %v", err)
	}
	conn.start(nil)
	defer func() {
		_ = conn.close()
		_ = cmd.Wait()
	}()

	data, err := conn.call(ctx, "s1", "get_state", nil)
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	t.Logf("state: %s", data)

	bash, err := conn.call(ctx, "b1", "bash", map[string]any{"command": "echo smoke"})
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	t.Logf("bash: %s", bash)

	entries, err := conn.call(ctx, "e1", "get_entries", nil)
	if err != nil {
		t.Fatalf("get_entries: %v", err)
	}
	t.Logf("entries: %s", entries)
}

// TestLiveSupervisedSession drives the whole Tier 1 loop through the Go
// supervisor against the real binary: spawn (with staged gate extension) →
// prompt → approval dialog parks → respond allow → settled idle → transcript
// shows the tool result → dismiss removes the card.
func TestLiveSupervisedSession(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cwd := t.TempDir()
	id, err := d.spawnSession(ctx, cwd, adapter.SpawnOptions{Prompt: "Run the shell command: echo live-supervised. Then reply OK."})
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}

	// Wait for the gate's approval dialog to park.
	var sess session.Session
	found := false
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id && s.Interaction != nil &&
				s.Interaction.Kind == session.InteractionPermission {
				sess, found = s, true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(time.Second)
	}
	if !found {
		t.Fatal("approval dialog never parked")
	}
	t.Logf("dialog: %q opts=%v", sess.Interaction.Message, sess.Interaction.Options)

	if err := d.respond(ctx, sess, api.RespondParams{Behavior: "allow"}); err != nil {
		t.Fatalf("respond: %v", err)
	}

	// Wait for idle.
	idle := false
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id && s.Status == session.StatusAwaitingInput &&
				(s.Interaction == nil || s.Interaction.Kind == session.InteractionIdle) {
				sess, idle = s, true
				break
			}
		}
		if idle {
			break
		}
		time.Sleep(time.Second)
	}
	if !idle {
		t.Fatal("session never settled idle")
	}

	// Transcript shows the tool result. The file appears once conversation
	// exists; fall back to polling briefly if the flush races us.
	var toolResult string
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if sess.TranscriptPath != "" {
			if view, err := readTranscriptView(sess.TranscriptPath, false); err == nil {
				for _, e := range view.Entries {
					if e.ToolName == "bash" && e.Result != "" {
						toolResult = e.Result
					}
				}
			}
		} else {
			for _, s := range reg.Snapshot() {
				if s.AgentSessionID == id && s.TranscriptPath != "" {
					sess = s
				}
			}
		}
		if toolResult != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if toolResult != "live-supervised\n" {
		t.Fatalf("tool result = %q", toolResult)
	}

	if err := d.dismiss(ctx, id); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	for _, s := range reg.Snapshot() {
		if s.AgentSessionID == id {
			t.Fatalf("session still listed: %+v", s)
		}
	}
}
