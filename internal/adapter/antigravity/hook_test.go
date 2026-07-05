package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestEventNamePrefersEnvelope(t *testing.T) {
	if got := EventName(HookEvent{Event: "Stop"}); got != "Stop" {
		t.Fatalf("EventName = %q, want Stop", got)
	}
}

func TestRescanOnHook(t *testing.T) {
	first, _ := json.Marshal(map[string]any{"invocationNum": 0})
	if !RescanOnHook(HookEvent{Event: "PreInvocation", Payload: first}) {
		t.Error("first PreInvocation (invocationNum 0) should trigger a rescan")
	}
	later, _ := json.Marshal(map[string]any{"invocationNum": 2})
	if RescanOnHook(HookEvent{Event: "PreInvocation", Payload: later}) {
		t.Error("later PreInvocation should not rescan")
	}
	if RescanOnHook(HookEvent{Event: "Stop"}) {
		t.Error("Stop should not rescan")
	}
}

func TestShouldBlockNever(t *testing.T) {
	for _, ev := range []string{"PreInvocation", "PreToolUse", "PostToolUse", "Stop"} {
		if ShouldBlock(HookEvent{Event: ev}) {
			t.Errorf("%s must not block", ev)
		}
	}
}

func TestHookOutput(t *testing.T) {
	cases := map[string]string{
		"Stop":          `{"decision":""}`,
		"PreInvocation": `{}`,
		"PostToolUse":   `{}`,
	}
	for event, want := range cases {
		if got := HookOutput(event); got != want {
			t.Errorf("HookOutput(%q) = %q, want %q", event, got, want)
		}
	}
}

func TestStatusForMapping(t *testing.T) {
	cases := map[string]session.Status{
		"PreInvocation": session.StatusWorking,
		"Stop":          session.StatusAwaitingInput,
	}
	for event, want := range cases {
		got, ok := statusFor(event)
		if !ok || got != want {
			t.Fatalf("statusFor(%q) = %q,%v; want %q", event, got, ok, want)
		}
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostInvocation"} {
		if _, ok := statusFor(event); ok {
			t.Errorf("statusFor(%q) should leave status unchanged", event)
		}
	}
}

func TestProcessHookStopAwaitsInput(t *testing.T) {
	reg := registry.New()
	ev := HookEvent{Agent: Agent, Event: "Stop", TmuxPane: "%1", Env: map[string]string{"ANTIGRAVITY_CONVERSATION_ID": "c1"}}
	s, alive := ProcessHook(reg, ev)
	if !alive || s.Status != session.StatusAwaitingInput {
		t.Fatalf("Stop: alive=%v status=%q; want awaiting_input", alive, s.Status)
	}
	if s.Interaction == nil || s.Interaction.Kind != session.InteractionIdle {
		t.Fatalf("Stop should set idle interaction, got %+v", s.Interaction)
	}
}

func TestProcessHookReadsConversationIDAndCwd(t *testing.T) {
	setupHome(t)
	reg := registry.New()
	payload, err := json.Marshal(map[string]any{
		"conversationId": "conv-x",
		"workspacePaths": []string{"/home/u/proj"},
		"transcriptPath": "/home/u/proj/.gemini/antigravity/transcript.jsonl",
	})
	if err != nil {
		t.Fatal(err)
	}
	ev := HookEvent{Agent: Agent, Event: "PreInvocation", TmuxPane: "%3", Payload: payload}
	s, alive := ProcessHook(reg, ev)
	if !alive {
		t.Fatal("session should exist")
	}
	if s.AgentSessionID != "conv-x" {
		t.Errorf("AgentSessionID = %q; want conv-x", s.AgentSessionID)
	}
	if s.Cwd != "/home/u/proj" {
		t.Errorf("Cwd = %q; want /home/u/proj", s.Cwd)
	}
	if want := transcriptPathFor("conv-x"); s.TranscriptPath != want {
		t.Errorf("TranscriptPath = %q; want canonical %q", s.TranscriptPath, want)
	}
}

// The hook and discovery must agree on the transcript path, else the periodic
// discovery scan wipes the hook's summary.
func TestProcessHookSummarySurvivesDiscovery(t *testing.T) {
	setupHome(t)
	convID := "conv-sum"
	canonical := transcriptPathFor(convID)
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"USER_INPUT","source":"USER","content":"<USER_REQUEST>Fix the bug</USER_REQUEST>","created_at":"2026-07-06T00:00:00Z","step_index":0}` + "\n"
	if err := os.WriteFile(canonical, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := registry.New()
	payload, _ := json.Marshal(map[string]any{
		"conversationId": convID,
		"workspacePaths": []string{"/home/u/proj"},
		"transcriptPath": "/home/u/proj/.gemini/antigravity/transcript.jsonl",
		"modelName":      "gemini-3-pro",
		"invocationNum":  0,
	})
	s, alive := ProcessHook(reg, HookEvent{Agent: Agent, Event: "PreInvocation", Payload: payload})
	if !alive {
		t.Fatal("session should exist")
	}
	if s.Summary == nil || s.Summary.Task == "" || s.Summary.ModelName == "" {
		t.Fatalf("hook should populate summary (task+model), got %+v", s.Summary)
	}

	// A discovery scan (which supplies no summary) must not wipe it.
	reg.ReconcileSessions(Agent, buildDiscovered(
		[]agyProc{{conversationID: convID, cwd: "/home/u/proj", transcriptPath: canonical}},
		map[string]paneInfo{},
	))
	got, ok := reg.Get(s.ID)
	if !ok {
		t.Fatal("session pruned after discovery")
	}
	if got.Summary == nil || got.Summary.Task == "" || got.Summary.ModelName == "" {
		t.Fatalf("discovery wiped the hook summary: %+v", got.Summary)
	}
}
