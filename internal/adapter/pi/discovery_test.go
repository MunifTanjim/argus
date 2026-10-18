package pi

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// stubSpawn replaces process launch with a fake RPC peer plus a real (sleep)
// child so dismiss/reap paths exercise real *exec.Cmd behavior.
func TestSpawnedProcessOutlivesTheRequest(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	var procCtx context.Context
	stubbed := spawnProc
	spawnProc = func(ctx context.Context, cwd, id, gate, model string) (*rpcConn, *exec.Cmd, error) {
		procCtx = ctx
		return stubbed(ctx, cwd, id, gate, model)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{}); err != nil {
		t.Fatal(err)
	}
	cancel()
	if procCtx.Err() != nil {
		t.Fatal("the pi process context ended with the request; the client disconnecting would kill pi")
	}
}

func TestSpawnReportsAnExitAtStart(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\necho 'Error: Failed to load extension \"gate.ts\": ParseError' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := piBin
	piBin = bin
	t.Cleanup(func() { piBin = old })
	// The exit can land after the first call fails; every spawn must see it.
	for range 20 {
		d := newDiscoverer(registry.New(), nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := d.spawnSession(ctx, t.TempDir(), adapter.SpawnOptions{})
		cancel()
		if err == nil || !strings.Contains(err.Error(), "Failed to load extension") {
			t.Fatalf("err = %v, want the exit at start with pi's reason", err)
		}
	}
}

func stubSpawn(t *testing.T, f *fakePi) {
	t.Helper()
	old := spawnProc
	t.Cleanup(func() { spawnProc = old })
	spawnProc = func(ctx context.Context, cwd, id, gate, model string) (*rpcConn, *exec.Cmd, error) {
		if gate == "" {
			t.Error("gate extension path empty")
		}
		fakeStdin, clientStdin := io.Pipe()
		clientStdout, fakeStdout := io.Pipe()
		f.mu.Lock()
		f.spawnModel = model
		f.mu.Unlock()
		go f.run(fakeStdin, fakeStdout)
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		return newRPCConn(clientStdin, clientStdout), cmd, nil
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
	id, err := d.spawnSession(ctx, "/work/proj", adapter.SpawnOptions{})
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
	id, err := d.spawnSession(ctx, "/work/proj", adapter.SpawnOptions{Prompt: "hello"})
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
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p := d.get(id)
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled","aborted":false}`))
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
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"confirm","title":"Allow bash?","message":"bash"}`))
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
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"select","title":"Allow?","options":["Allow","Block"]}`))
	d.handleEvent(id, "agent_start", json.RawMessage(`{"type":"agent_start"}`))
	s := snapshotByID(t, reg, id)
	if s.Interaction == nil || s.Interaction.RequestID != "u1" {
		t.Fatalf("dialog cleared by trailing activity: %+v", s.Interaction)
	}
	_ = f
}

func TestExtensionDialogsAreQuestions(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(raw string) session.Session {
		d.handleEvent(id, "extension_ui_request", json.RawMessage(raw))
		s := snapshotByID(t, reg, id)
		if s.Interaction == nil || s.Interaction.Kind != session.InteractionQuestion || len(s.Interaction.Questions) != 1 {
			t.Fatalf("interaction = %+v, want one question", s.Interaction)
		}
		return s
	}
	answer := func(s session.Session, label string) api.RespondParams {
		q := s.Interaction.Questions[0].Question
		return api.RespondParams{RequestID: s.Interaction.RequestID, Behavior: "allow", Answers: map[string]any{q: label}}
	}

	s := ask(`{"type":"extension_ui_request","id":"u1","method":"select","title":"Pick a branch","options":["main","dev"]}`)
	if q := s.Interaction.Questions[0]; !slices.Equal(q.Options, []string{"main", "dev"}) || s.Interaction.AllowUnanswered {
		t.Fatalf("select question = %+v, want its real options and an answer required", q)
	}
	if err := d.respond(ctx, s, answer(s, "staging")); err == nil {
		t.Fatal("an answer that is not an option must fail")
	}
	if err := d.respond(ctx, s, answer(s, "dev")); err != nil {
		t.Fatal(err)
	}
	if got := f.waitUIResponses(1); got[0]["id"] != "u1" || got[0]["value"] != "dev" {
		t.Fatalf("ui responses = %v, want the chosen option", got)
	}

	s = ask(`{"type":"extension_ui_request","id":"u2","method":"input","title":"Commit message"}`)
	if !s.Interaction.AllowUnanswered {
		t.Fatal("an input dialog must take an empty answer")
	}
	if err := d.respond(ctx, s, api.RespondParams{RequestID: "u2", Behavior: "allow", Answers: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitUIResponses(2); got[1]["value"] != "" || got[1]["cancelled"] != nil {
		t.Fatalf("ui responses = %v, want an empty value, not a cancel", got)
	}

	s = ask(`{"type":"extension_ui_request","id":"u3","method":"editor","title":"Edit plan"}`)
	if err := d.respond(ctx, s, api.RespondParams{RequestID: "u3", QuestionAction: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitUIResponses(3); got[2]["cancelled"] != true {
		t.Fatalf("ui responses = %v, want a cancel", got)
	}
}

func TestPromptForACommandWithDialogsReturns(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t, promptDisposition: "handled", promptRelease: make(chan struct{})}
	stubSpawn(t, f)
	old := quickFail
	quickFail = 50 * time.Millisecond
	t.Cleanup(func() { quickFail = old })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled"}`))

	// pi holds the answer while the command waits on its dialogs.
	if err := d.sendPrompt(ctx, id, "/pick"); err != nil {
		t.Fatalf("sendPrompt = %v, want it back before the command ends", err)
	}
	close(f.promptRelease)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := snapshotByID(t, reg, id); s.Status == session.StatusAwaitingInput && s.Interaction != nil && s.Interaction.Kind == session.InteractionIdle {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("card = %+v after the command ended with no run, want idle", snapshotByID(t, reg, id))
}

func TestExpiredDialogLeavesTheCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"select","title":"Pick","options":["a"],"timeout":50}`))
	if s := snapshotByID(t, reg, id); s.Interaction == nil || s.Interaction.RequestID != "u1" {
		t.Fatalf("interaction = %+v, want the dialog", s.Interaction)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := snapshotByID(t, reg, id); s.Interaction == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the expired dialog stayed on the card")
}

func TestGateApprovalCarriesTheDenyReason(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(uiID string) session.Session {
		d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"`+uiID+`","method":"input","title":"Allow bash?","placeholder":"`+approvalMarker+`bash rm -rf build"}`))
		s := snapshotByID(t, reg, id)
		if s.Interaction == nil || s.Interaction.Kind != session.InteractionPermission || s.Interaction.ToolName != "bash rm -rf build" {
			t.Fatalf("interaction = %+v, want a permission prompt for the tool", s.Interaction)
		}
		return s
	}

	if err := d.respond(ctx, ask("u1"), api.RespondParams{Behavior: "deny", Reason: "use make clean"}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitUIResponses(1); len(got) != 1 || got[0]["value"] != `{"allow":false,"reason":"use make clean"}` {
		t.Fatalf("ui responses = %v, want a deny carrying the reason", got)
	}
	if err := d.respond(ctx, ask("u2"), api.RespondParams{Behavior: "allow"}); err != nil {
		t.Fatal(err)
	}
	if got := f.waitUIResponses(2); len(got) != 2 || got[1]["value"] != `{"allow":true}` {
		t.Fatalf("ui responses = %v, want an allow", got)
	}
}

func TestRespondAllowDenies(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sess := snapshotByID(t, reg, id)

	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"confirm","title":"Allow bash?"}`))
	if err := d.respond(ctx, sess, api.RespondParams{Behavior: "allow"}); err != nil {
		t.Fatalf("respond allow: %v", err)
	}
	got := f.waitUIResponses(1)
	if len(got) != 1 || got[0]["id"] != "u1" || got[0]["confirmed"] != true {
		t.Fatalf("ui responses = %v", got)
	}

	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"u2","method":"select","title":"Allow?","options":["Allow","Block"]}`))
	if err := d.respond(ctx, sess, api.RespondParams{Behavior: "deny", RequestID: "u2"}); err != nil {
		t.Fatalf("respond deny: %v", err)
	}
	got = f.waitUIResponses(2)
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
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
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
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
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

func TestSpawnReportsModelAndEffort(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t, state: map[string]any{
		"model":         map[string]any{"id": "claude-sonnet-4-5", "reasoning": true},
		"thinkingLevel": "medium",
	}}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := snapshotByID(t, reg, id)
	if s.Summary == nil || s.Summary.ModelName != "claude-sonnet-4-5" || s.Summary.Effort != "medium" {
		t.Fatalf("summary = %+v", s.Summary)
	}
	d.handleEvent(id, "thinking_level_changed", json.RawMessage(`{"type":"thinking_level_changed","level":"high"}`))
	if s := snapshotByID(t, reg, id); s.Summary.Effort != "high" {
		t.Fatalf("effort after change = %q", s.Summary.Effort)
	}
}

func TestModelWithoutReasoningTakesNoEffort(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t, state: map[string]any{
		"model":         map[string]any{"id": "gpt-4o", "reasoning": false},
		"thinkingLevel": "off",
	}}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := snapshotByID(t, reg, id)
	if s.Summary == nil || s.Summary.ModelName != "gpt-4o" || s.Summary.Effort != "" {
		t.Fatalf("summary = %+v", s.Summary)
	}
}

func TestSettledRereadsModel(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t, state: map[string]any{
		"model": map[string]any{"id": "m1", "reasoning": true}, "thinkingLevel": "low",
	}}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.state["model"] = map[string]any{"id": "m2", "reasoning": true}
	f.mu.Unlock()
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled"}`))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := snapshotByID(t, reg, id); s.Summary != nil && s.Summary.ModelName == "m2" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("model never refreshed after settle")
}

func TestSpawnPassesModel(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{Model: "anthropic/claude-sonnet-4-5"}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.spawnModel != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("spawn model = %q", f.spawnModel)
	}
}

func TestLiveSessionGetsNoResumePane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg, nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &piAdapter{disc: d}
	if _, _, ok := a.ResumeCommand(id); ok {
		t.Fatal("a live owned session must not get a second pi process")
	}
	if _, _, ok := a.ResumeCommand("past-session"); !ok {
		t.Fatal("a past session must stay resumable")
	}
	if err := d.dismiss(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := a.ResumeCommand(id); !ok {
		t.Fatal("a dismissed session must be resumable again")
	}
}
