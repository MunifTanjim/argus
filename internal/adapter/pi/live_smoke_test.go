//go:build pi_live

package pi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
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

	useRealRelay(t)
	conn, err := spawnHosted(ctx, t.TempDir(), "smoke-"+newSessionID(), "", "")
	if err != nil {
		t.Fatalf("spawnHosted: %v", err)
	}
	conn.start(nil)
	defer func() {
		_ = conn.stop()
		<-conn.done
		_ = conn.close()
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
// prompt → settled idle → transcript
// shows the tool result → dismiss removes the card.
func TestLiveSupervisedSession(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	useRealRelay(t)
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cwd := t.TempDir()
	id, err := d.spawnSession(ctx, cwd, adapter.SpawnOptions{Prompt: "Run the shell command: echo live-supervised. Then reply OK."})
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}

	var sess session.Session

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

// Run with: go test ./internal/adapter/pi/ -tags pi_live -run TestLiveCompletions -v
func TestLiveCompletions(t *testing.T) {
	if _, err := exec.LookPath(piBin); err != nil {
		t.Skipf("pi binary not available: %v", err)
	}
	agentDir := t.TempDir()
	writeFile := func(rel, body string) {
		path := filepath.Join(agentDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("prompts/zz-live.md", "---\ndescription: Live template\n---\nSay hi.\n")
	writeFile("skills/zz-skill/SKILL.md", "---\nname: zz-skill\ndescription: Live skill\n---\nDo nothing.\n")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)

	useRealRelay(t)
	d := newDiscoverer(registry.New(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, err := d.spawnSession(ctx, t.TempDir(), adapter.SpawnOptions{})
	if err != nil {
		t.Fatalf("spawnSession: %v", err)
	}
	got, err := d.completions(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("completions: %+v", got)
	want := map[string]string{"compact": adapter.CompactCompletion.Description, "new": newCompletion.Description, "zz-live": "Live template", "skill:zz-skill": "Live skill"}
	for _, it := range got {
		if desc, ok := want[it.Name]; ok && desc == it.Description {
			delete(want, it.Name)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing or wrong: %v", want)
	}
}
