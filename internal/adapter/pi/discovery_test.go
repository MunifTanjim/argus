package pi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// stubSpawn replaces process launch with a fake RPC peer plus a real (sleep)
// child so dismiss/reap paths exercise real *exec.Cmd behavior.
func TestSpawnedProcessOutlivesTheRequest(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	var procCtx context.Context
	stubbed := spawnProc
	spawnProc = func(ctx context.Context, cwd, id, model string) (*rpcConn, *exec.Cmd, error) {
		procCtx = ctx
		return stubbed(ctx, cwd, id, model)
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
		d := newDiscoverer(registry.New())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := d.spawnSession(ctx, t.TempDir(), adapter.SpawnOptions{})
		cancel()
		if err == nil || !strings.Contains(err.Error(), "Failed to load extension") {
			t.Fatalf("err = %v, want the exit at start with pi's reason", err)
		}
	}
}

func (d *discoverer) handleEvent(id, typ string, raw json.RawMessage) {
	if p := d.get(id); p != nil {
		d.procEvent(p, typ, raw)
	}
}

func isIdle(s session.Session) bool {
	return s.Status == session.StatusAwaitingInput && s.Interaction != nil && s.Interaction.Kind == session.InteractionIdle
}

func stubSpawn(t *testing.T, f *fakePi) {
	t.Helper()
	old := spawnProc
	t.Cleanup(func() { spawnProc = old })
	spawnProc = func(ctx context.Context, cwd, id, model string) (*rpcConn, *exec.Cmd, error) {
		fakeStdin, clientStdin := io.Pipe()
		clientStdout, fakeStdout := io.Pipe()
		f.mu.Lock()
		f.spawnModel = model
		// Like real pi, get_state reports the --session-id until /new.
		if f.state == nil {
			f.state = map[string]any{}
		}
		if _, ok := f.state["sessionId"]; !ok {
			f.state["sessionId"] = id
		}
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	if s.Interaction.Message != "Allow bash?\n\nbash" || s.Interaction.ToolName != "" {
		t.Fatalf("message = %q, tool = %q", s.Interaction.Message, s.Interaction.ToolName)
	}
	if deny := s.Interaction.Options[1]; deny.Placeholder != "" {
		t.Fatalf("deny asks for a reason pi cannot receive: %q", deny.Placeholder)
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
	d := newDiscoverer(reg)
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

func TestSettledRunDropsItsAbortedDialog(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	stubSpawn(t, &fakePi{t: t})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"idle","method":"confirm","title":"Keep?"}`))
	d.handleEvent(id, "agent_start", json.RawMessage(`{"type":"agent_start"}`))
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"run","method":"confirm","title":"Run?"}`))
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled","aborted":true}`))
	p := d.get(id)
	p.mu.Lock()
	_, idle := p.pending["idle"]
	_, run := p.pending["run"]
	p.mu.Unlock()
	if !idle || run {
		t.Fatalf("pending idle=%v run=%v, want only the dialog opened outside the run", idle, run)
	}

	d.dropOwnedPending(p, "idle")
	d.handleEvent(id, "agent_start", json.RawMessage(`{"type":"agent_start"}`))
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"run2","method":"confirm","title":"Run?"}`))
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled","aborted":true}`))
	if s := snapshotByID(t, reg, id); s.Status != session.StatusAwaitingInput || s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("card = %v %+v, want idle", s.Status, s.Interaction)
	}
}

func TestExtensionDialogsAreQuestions(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	waitCardState(t, reg, id, isIdle)
}

func TestCompactRunsThroughRPC(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.sendPrompt(ctx, id, " /compact\nkeep the tests "); err != nil {
		t.Fatal(err)
	}
	if c := f.call("compact"); c == nil || c["customInstructions"] != "keep the tests" {
		t.Fatalf("compact = %v, want the instructions", c)
	}
	f.mu.Lock()
	prompts := f.prompts
	f.mu.Unlock()
	if len(prompts) != 0 {
		t.Fatalf("prompts = %v, want none: pi keeps /compact out of prompts", prompts)
	}
	waitCardState(t, reg, id, isIdle)

	if err := d.sendPrompt(ctx, id, "/compact"); err != nil {
		t.Fatal(err)
	}
	if c := f.call("compact"); c == nil || c["customInstructions"] != nil {
		t.Fatalf("compact = %v, want no instructions", c)
	}
}

func TestCompactionShowsWorkingAfterTheAbortSettles(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d.handleEvent(id, "agent_start", json.RawMessage(`{"type":"agent_start"}`))
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled","aborted":true}`))
	waitCardState(t, reg, id, isIdle)
	d.handleEvent(id, "compaction_start", json.RawMessage(`{"type":"compaction_start","reason":"manual"}`))
	waitCard(t, reg, id, session.StatusWorking)
}

func TestStaleStateKeepsTheNewSession(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hold, held := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.stateHold, f.stateHeld = hold, held
	f.mu.Unlock()
	// The abort that /new starts with settles the run: that get_state answers
	// with the old id, and arrives after /new moved the process.
	d.handleEvent(id, "agent_settled", json.RawMessage(`{"type":"agent_settled","aborted":true}`))
	<-held
	f.mu.Lock()
	f.state["sessionId"], f.state["sessionFile"] = "ses-new", "/tmp/ses-new.jsonl"
	f.mu.Unlock()
	if _, err := d.runShell(ctx, id, "/new"); err != nil {
		t.Fatal(err)
	}
	waitCardState(t, reg, "ses-new", isIdle)
	close(hold)

	time.Sleep(200 * time.Millisecond)
	if d.get(id) != nil || d.get("ses-new") == nil {
		t.Fatal("a stale get_state must not move the process back")
	}
	snapshotGone(t, reg, id)
	if s := snapshotByID(t, reg, "ses-new"); s.Status == session.StatusDead || s.TranscriptPath != "/tmp/ses-new.jsonl" {
		t.Fatalf("card = %+v, want the new session live on its own file", s)
	}
}

func TestNewSessionFollowsTheNewID(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.state["sessionId"], f.state["sessionFile"] = "ses-new", "/tmp/ses-new.jsonl"
	f.mu.Unlock()
	if _, err := d.runShell(ctx, id, "/new"); err != nil {
		t.Fatal(err)
	}
	if f.call("new_session") == nil {
		t.Fatal("new_session never sent")
	}
	s := waitCardState(t, reg, "ses-new", isIdle)
	if s.TranscriptPath != "/tmp/ses-new.jsonl" {
		t.Fatalf("transcript = %q, want the new session's file", s.TranscriptPath)
	}
	snapshotGone(t, reg, id)
	if d.get(id) != nil || d.get("ses-new") == nil {
		t.Fatal("the process must be tracked under the new id only")
	}
	// Events and prompts follow the process to its new session.
	d.get("ses-new").conn.onEvent("agent_start", json.RawMessage(`{"type":"agent_start"}`))
	waitCard(t, reg, "ses-new", session.StatusWorking)
	if err := d.sendPrompt(ctx, "ses-new", "hi"); err != nil {
		t.Fatal(err)
	}
}

func TestTwoSwitchesBeforeAPushKeepTheCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rec := snapshotByID(t, reg, id).ID
	p := d.get(id)
	f.mu.Lock()
	f.quietState = true
	f.mu.Unlock()
	for _, next := range []string{"ses-b", "ses-c"} {
		f.mu.Lock()
		f.state["sessionId"] = next
		f.mu.Unlock()
		if _, err := d.loadState(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	d.upsert(p, "", nil, false)
	s, ok := reg.Get(rec)
	if !ok {
		t.Fatalf("card %s dropped: %+v", rec, reg.Snapshot())
	}
	if s.AgentSessionID != "ses-c" {
		t.Fatalf("agent session = %q, want ses-c", s.AgentSessionID)
	}
}

func TestNewSessionKeepsTheCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rec := snapshotByID(t, reg, id).ID
	events, stop := reg.Subscribe()
	defer stop()
	f.mu.Lock()
	f.state["sessionId"] = "ses-new"
	f.mu.Unlock()
	if _, err := d.runShell(ctx, id, "/new"); err != nil {
		t.Fatal(err)
	}
	if s := waitCardState(t, reg, "ses-new", isIdle); s.ID != rec {
		t.Fatalf("card id = %q, want %q", s.ID, rec)
	}
	if _, ok := reg.Get(rec); !ok {
		t.Fatal("card dropped")
	}
	p := d.get("ses-new")
	p.mu.Lock()
	prev := p.prevID
	p.mu.Unlock()
	if prev != "" {
		t.Fatalf("prevID = %q after the push that carried it", prev)
	}
	for {
		select {
		case ev := <-events:
			if ev.Session.ID == rec && (ev.Type == registry.EventRemoved || ev.Session.Status == session.StatusDead) {
				t.Fatalf("card ended across /new: %+v", ev)
			}
			continue
		default:
		}
		break
	}
}

func TestNewSessionCancelledByAnExtension(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.newSessionCancelled = true
	f.mu.Unlock()
	// An answered dialog of the extension leaves the card working.
	d.upsert(d.get(id), session.StatusWorking, nil, false)
	if _, err := d.runShell(ctx, id, "/new"); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v, want the cancel reported", err)
	}
	if d.get(id) == nil {
		t.Fatal("a cancelled /new must keep the session")
	}
	waitCardState(t, reg, id, isIdle)
}

func TestExitAfterNewSessionEndsTheNewCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.state["sessionId"] = "ses-new"
	f.mu.Unlock()
	if _, err := d.runShell(ctx, id, "/new"); err != nil {
		t.Fatal(err)
	}
	waitCardState(t, reg, "ses-new", isIdle)
	_ = d.get("ses-new").cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for d.get("ses-new") != nil {
		if time.Now().After(deadline) {
			t.Fatal("the exited process was never dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshotGone(t, reg, "ses-new")
}

func TestExpiredDialogLeavesTheCard(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
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

func TestRespondAllowDenies(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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
	d := newDiscoverer(reg)
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

func TestRenameOwnedSetsSessionName(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.rename(ctx, id, "fix login"); err != nil {
		t.Fatal(err)
	}
	if c := f.call("set_session_name"); c == nil || c["name"] != "fix login" {
		t.Fatalf("set_session_name = %v", c)
	}
	// A later card push keeps the new name.
	d.handleEvent(id, "agent_start", json.RawMessage(`{"type":"agent_start"}`))
	if s := snapshotByID(t, reg, id); s.Name != "fix login" {
		t.Fatalf("name = %q", s.Name)
	}
}

func TestQueueFollowsQueueUpdates(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &piAdapter{disc: d}
	st := a.NewStreamingTranscript("/tmp/ses-1.jsonl", "/tmp/ses-1.jsonl", false).(adapter.QueueSource)
	if q := st.Queue(); len(q) != 0 {
		t.Fatalf("initial queue = %v", q)
	}
	d.handleEvent(id, "queue_update", json.RawMessage(`{"type":"queue_update","steering":["one"],"followUp":["two"]}`))
	want := []transcript.QueuedPrompt{{Text: "one"}, {Text: "two"}}
	if q := st.Queue(); !reflect.DeepEqual(q, want) {
		t.Fatalf("queue = %v", q)
	}
	st.ClearQueue()
	if q := st.Queue(); len(q) != 0 {
		t.Fatalf("queue after clear = %v", q)
	}
	d.handleEvent(id, "queue_update", json.RawMessage(`{"type":"queue_update","steering":["three"],"followUp":[]}`))
	if q := st.Queue(); !reflect.DeepEqual(q, []transcript.QueuedPrompt{{Text: "three"}}) {
		t.Fatalf("queue after new update = %v", q)
	}
	d.handleEvent(id, "queue_update", json.RawMessage(`{"type":"queue_update","steering":[],"followUp":[]}`))
	if q := st.Queue(); len(q) != 0 {
		t.Fatalf("drained queue = %v", q)
	}
}

func TestSendPromptRefusesPendingInteraction(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &piAdapter{disc: d}
	if !a.QueuesPrompts(snapshotByID(t, reg, id)) {
		t.Fatal("pi must take prompts mid-run")
	}
	d.handleEvent(id, "extension_ui_request", json.RawMessage(`{"type":"extension_ui_request","id":"u1","method":"confirm","title":"Allow bash?","message":"bash"}`))
	err = a.SendPrompt(ctx, snapshotByID(t, reg, id), "hi")
	if !errors.Is(err, api.ErrSessionBusy) {
		t.Fatalf("err = %v, want busy", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) != 0 {
		t.Fatalf("prompt reached pi: %v", f.prompts)
	}
}

func TestShellInputRunsBash(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := &piAdapter{disc: d}
	if !a.IsShellInput("!ls") || a.IsShellInput("ls") || !a.IsShellInput(" /new ") || a.IsShellInput("/new foo") || a.IsShellInput("/newer") {
		t.Fatal("IsShellInput must match only a leading ! and a bare /new")
	}
	for _, tc := range []struct {
		text    string
		command string
		exclude bool
	}{
		{"! ls -la", "ls -la", false},
		{"!!git status", "git status", true},
	} {
		if submit, err := a.SendShellInput(ctx, snapshotByID(t, reg, id), tc.text); err != nil || submit {
			t.Fatalf("%q: submit = %v, err = %v", tc.text, submit, err)
		}
		c := f.call("bash")
		if c == nil || c["command"] != tc.command || c["excludeFromContext"] != tc.exclude {
			t.Fatalf("%q: bash = %v", tc.text, c)
		}
	}
	if _, err := a.SendShellInput(ctx, snapshotByID(t, reg, id), "!  "); err == nil {
		t.Fatal("empty command must fail")
	}
	waitCardState(t, reg, id, isIdle)
}

func TestShellInputRunsNewSession(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.state["sessionId"] = "ses-new"
	f.mu.Unlock()
	a := &piAdapter{disc: d}
	if submit, err := a.SendShellInput(ctx, snapshotByID(t, reg, id), "/new"); err != nil || submit {
		t.Fatalf("submit = %v, err = %v, want /new run over RPC", submit, err)
	}
	if f.call("new_session") == nil {
		t.Fatal("new_session never sent")
	}
	waitCardState(t, reg, "ses-new", isIdle)
}

func TestLiveSessionGetsNoResumePane(t *testing.T) {
	reg := registry.New()
	d := newDiscoverer(reg)
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

func waitCard(t *testing.T, reg *registry.Registry, id string, want session.Status) session.Session {
	t.Helper()
	return waitCardState(t, reg, id, func(s session.Session) bool { return want == "" || s.Status == want })
}

func waitCardState(t *testing.T, reg *registry.Registry, id string, ok func(session.Session) bool) session.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range reg.Snapshot() {
			if s.AgentSessionID == id && ok(s) {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("card for %s never matched: %+v", id, reg.Snapshot())
	return session.Session{}
}
