package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
)

func TestAdapterIsHeadlessWithCapabilities(t *testing.T) {
	a := New()
	if !a.IsHeadless() {
		t.Fatal("codex adapter must be headless")
	}
	if a.Agent() != "codex" {
		t.Fatalf("Agent = %q", a.Agent())
	}
	for name, ok := range map[string]bool{
		"Responder": implements[adapter.Responder](a),
		"Prompter":  implements[adapter.Prompter](a),
		"Spawner":   implements[adapter.Spawner](a),
		"Dismisser": implements[adapter.Dismisser](a),
	} {
		if !ok {
			t.Errorf("codex adapter must implement %s", name)
		}
	}
	if name, _ := a.SpawnCommand("x"); name != "codex" {
		t.Errorf("SpawnCommand binary = %q; want codex (PATH probe)", name)
	}
	if name, args, ok := a.ResumeCommand("t1"); !ok || name != "codex" || len(args) != 2 || args[0] != "resume" || args[1] != "t1" {
		t.Errorf("ResumeCommand = %q %v %v", name, args, ok)
	}
	if a.ShouldBlock(adapter.HookEvent{Event: "PermissionRequest"}) {
		t.Error("hooks are gone: nothing blocks")
	}
	if len(a.DefaultHookEvents()) != 0 {
		t.Error("no hook events are installed")
	}
}

func implements[T any](a adapter.Adapter) bool { _, ok := a.(T); return ok }

// A thread with no turn yet has no rollout file, and `codex resume <id>` exits
// with "no rollout found", so no viewer is offered until the file exists.
func TestResumeCommandWaitsForRollout(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	a := New().(*cxAdapter)
	a.NewDiscoverer(registry.New(), nil)
	rollout := filepath.Join(t.TempDir(), "rollout-t1.jsonl")
	a.disc.threads["t1"] = &threadEntry{cwd: "/w", path: rollout}

	if _, _, ok := a.ResumeCommand("t1"); ok {
		t.Fatal("ResumeCommand must be unavailable before the rollout file exists")
	}
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if name, args, ok := a.ResumeCommand("t1"); !ok || name != "codex" || len(args) != 2 || args[1] != "t1" {
		t.Fatalf("after rollout exists: ResumeCommand = %q %v %v", name, args, ok)
	}
	// The node probes support with an empty id, and history resumes untracked ids.
	if _, _, ok := a.ResumeCommand(""); !ok {
		t.Error("empty-id probe must report resume support")
	}
	if _, _, ok := a.ResumeCommand("untracked"); !ok {
		t.Error("untracked (history) threads must stay resumable")
	}
}
