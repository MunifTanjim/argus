package pi

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// stubSpawn replaces process launch with a fake RPC peer plus a real (sleep)
// child so dismiss/reap paths exercise real *exec.Cmd behavior.
func stubSpawn(t *testing.T, f *fakePi) {
	t.Helper()
	old := spawnProc
	t.Cleanup(func() { spawnProc = old })
	spawnProc = func(ctx context.Context, cwd, id, gate string) (*rpcConn, *exec.Cmd, error) {
		if gate == "" {
			t.Error("gate extension path empty")
		}
		fakeStdin, clientStdin := io.Pipe()
		clientStdout, fakeStdout := io.Pipe()
		go f.run(fakeStdin, fakeStdout)
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return newRPCConn(clientStdin, clientStdout, nil), cmd, nil
	}
	oldKill := dismissKillAfter
	dismissKillAfter = 200 * time.Millisecond
	t.Cleanup(func() { dismissKillAfter = oldKill })
}

func snapshotByID(t *testing.T, reg *registry.Registry, id string) session.Session {
	t.Helper()
	for _, s := range reg.Snapshot() {
		if s.AgentSessionID == id {
			return s
		}
	}
	t.Fatalf("session %s not in registry", id)
	return session.Session{}
}

func TestSpawnTracksWorkingSession(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/work/proj", "")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if id == "" {
		t.Fatal("empty session id")
	}
	s := snapshotByID(t, reg, id)
	if s.Agent != Agent || s.Status != session.StatusWorking {
		t.Fatalf("card = %+v", s)
	}
	if s.TranscriptPath != "/tmp/ses-1.jsonl" {
		t.Fatalf("transcript path = %q", s.TranscriptPath)
	}
	if s.Cwd != "/work/proj" {
		t.Fatalf("cwd = %q", s.Cwd)
	}
}

func TestSpawnWithPromptSends(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/work/proj", "hello")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	snapshotByID(t, reg, id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) != 1 || f.prompts[0]["message"] != "hello" {
		t.Fatalf("prompts = %v", f.prompts)
	}
}

func TestAgentSettledIdles(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	p := d.get(id)
	d.handleEvent(id, json.RawMessage(`{"type":"agent_settled","aborted":false}`))
	_ = f
	s := snapshotByID(t, reg, id)
	if s.Status != session.StatusAwaitingInput {
		t.Fatalf("status = %v", s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("interaction = %+v", s.Interaction)
	}
	p.mu.Lock()
	streaming := p.streaming
	p.mu.Unlock()
	if streaming {
		t.Fatal("still streaming after settled")
	}
}

func TestUIDialogParksPermission(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"confirm","title":"Allow bash?","message":"bash"}`))
	s := snapshotByID(t, reg, id)
	if s.Status != session.StatusAwaitingInput {
		t.Fatalf("status = %v", s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("interaction = %+v", s.Interaction)
	}
	if s.Interaction.RequestID != "u1" {
		t.Fatalf("request id = %q", s.Interaction.RequestID)
	}
	p := d.get(id)
	p.mu.Lock()
	n := len(p.pending)
	p.mu.Unlock()
	if n != 1 {
		t.Fatalf("pending = %d", n)
	}
	_ = f
}

func TestActivityDuringDialogKeepsPrompt(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"select","title":"Allow?","options":["Allow","Block"]}`))
	d.handleEvent(id, json.RawMessage(`{"type":"agent_start"}`))
	s := snapshotByID(t, reg, id)
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission {
		t.Fatalf("dialog cleared by trailing activity: %+v", s.Interaction)
	}
	_ = f
}

func TestRespondAllowDenies(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	sess := snapshotByID(t, reg, id)

	d.handleEvent(id, json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"confirm","title":"Allow bash?"}`))
	if err := d.respond(ctx, sess, api.RespondParams{Behavior: "allow"}); err != nil {
		t.Fatalf("respond allow: %v", err)
	}
	got := f.uiResponses()
	if len(got) != 1 || got[0]["id"] != "u1" || got[0]["confirmed"] != true {
		t.Fatalf("ui responses = %v", got)
	}

	d.handleEvent(id, json.RawMessage(`{"type":"extension_ui_request","id":"u2","method":"select","title":"Allow?","options":["Allow","Block"]}`))
	if err := d.respond(ctx, sess, api.RespondParams{Behavior: "deny", RequestID: "u2"}); err != nil {
		t.Fatalf("respond deny: %v", err)
	}
	got = f.uiResponses()
	if len(got) != 2 || got[1]["id"] != "u2" || got[1]["cancelled"] != true {
		t.Fatalf("ui responses = %v", got)
	}
	p := d.get(id)
	p.mu.Lock()
	n := len(p.pending)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestRespondUnknownIsError(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	sess := snapshotByID(t, reg, id)
	if err := d.respond(ctx, sess, api.RespondParams{}); err == nil {
		t.Fatal("expected error with no pending dialog")
	}
	_ = f
}

func TestDismissKillsAndMarksDead(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", "")
	if err != nil {
		t.Fatal(err)
	}
	p := d.get(id)
	if p == nil {
		t.Fatal("proc not tracked after spawn")
	}
	if err := d.dismiss(ctx, id); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if d.get(id) != nil {
		t.Fatal("proc still tracked")
	}
	snapshotGone(t, reg, id)
	_ = f
}

func snapshotGone(t *testing.T, reg *registry.Registry, id string) {
	t.Helper()
	for _, s := range reg.Snapshot() {
		if s.AgentSessionID == id {
			t.Fatalf("session %s still listed: %+v", id, s)
		}
	}
}
