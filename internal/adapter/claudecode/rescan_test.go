package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
)

func hookEvent(name string) adapter.HookEvent {
	return adapter.HookEvent{Agent: Agent, Event: name, Payload: json.RawMessage(`{}`)}
}

func TestRescanOnHook(t *testing.T) {
	a := New()
	// SessionStart is the point of the rescan: a pane argus has never seen.
	if !a.RescanOnHook(hookEvent("SessionStart")) {
		t.Error("SessionStart must trigger a rescan")
	}
	// SessionEnd must not. ProcessHook has already removed the session, and the
	// agent is still alive while its own exit hook runs — so the scan would find
	// the pane and re-create what the hook just ended.
	if a.RescanOnHook(hookEvent("SessionEnd")) {
		t.Error("SessionEnd must not trigger a rescan")
	}
	if a.RescanOnHook(hookEvent("PostToolUse")) {
		t.Error("per-tool-call hooks must not trigger a rescan")
	}
}
