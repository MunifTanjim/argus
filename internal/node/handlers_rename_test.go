package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/tmux"
)

type fakeRenamer struct {
	adapter.Adapter
	names   map[string]string
	err     error
	pending bool // the agent takes the name later
}

func (f *fakeRenamer) RenameSession(_ context.Context, sess session.Session, name string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.names == nil {
		f.names = map[string]string{}
	}
	f.names[sess.AgentSessionID] = name
	return !f.pending, nil
}

func renameTestNode(t *testing.T, agent string, r adapter.Adapter) (*Node, session.Session) {
	t.Helper()
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	d.adapters[agent] = r
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent:              agent,
		AgentSessionID:     "ses_1",
		Name:               "Old",
		Status:             session.StatusAwaitingInput,
		Interaction:        &session.Interaction{Kind: session.InteractionPermission, ToolName: "Bash"},
		ReplaceInteraction: true,
	})
	return d, s
}

func callRename(d *Node, id, name string) (api.SessionRenameResult, error) {
	var res api.SessionRenameResult
	params, _ := json.Marshal(api.SessionRenameParams{SessionID: id, Name: name})
	raw, err := d.handleSessionRename(context.Background(), params)
	if err != nil {
		return res, err
	}
	return raw.(api.SessionRenameResult), nil
}

func TestHandleSessionRenameRenamesThroughTheAgent(t *testing.T) {
	fr := &fakeRenamer{}
	d, s := renameTestNode(t, "opencode", fr)

	res, err := callRename(d, s.ID, "  New name  ")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if res.Name != "New name" || !res.Applied {
		t.Errorf("result = %+v, want the trimmed New name, applied", res)
	}
	if fr.names["ses_1"] != "New name" {
		t.Errorf("agent saw %q, want the trimmed New name", fr.names["ses_1"])
	}
	got, _ := d.reg.Get(s.ID)
	if got.Name != "New name" {
		t.Errorf("registry name = %q, want New name", got.Name)
	}
	if got.Interaction == nil || got.Interaction.ToolName != "Bash" {
		t.Errorf("interaction = %+v, want the pending prompt preserved", got.Interaction)
	}
}

func TestHandleSessionRenameRejectsBadInput(t *testing.T) {
	fr := &fakeRenamer{}
	d, s := renameTestNode(t, "opencode", fr)

	for _, tc := range []struct {
		name  string
		id    string
		blank string
	}{
		{"blank name", s.ID, "   "},
		{"empty name", s.ID, ""},
		{"missing id", "", "x"},
		{"unknown session", "missing", "x"},
		{"newline in name", s.ID, "a\nb"},
	} {
		if _, err := callRename(d, tc.id, tc.blank); err == nil {
			t.Errorf("%s: want an error", tc.name)
		}
	}
	if len(fr.names) != 0 {
		t.Errorf("agent saw renames %v, want none on invalid input", fr.names)
	}
}

// An agent that takes the name later leaves the registry to its own report.
func TestHandleSessionRenamePendingLeavesTheRegistry(t *testing.T) {
	fr := &fakeRenamer{pending: true}
	d, s := renameTestNode(t, "claude", fr)

	res, err := callRename(d, s.ID, "New name")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if res.Name != "New name" || res.Applied {
		t.Errorf("result = %+v, want New name, not applied", res)
	}
	if got, _ := d.reg.Get(s.ID); got.Name != "Old" {
		t.Errorf("registry name = %q, want Old until the agent reports the rename", got.Name)
	}
}

func TestHandleSessionRenameReportsUnsupportedAgents(t *testing.T) {
	// antigravity wires the real adapter, which has no rename feature.
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent:          "antigravity",
		AgentSessionID: "c1",
		Status:         session.StatusIdle,
	})
	_, err := callRename(d, s.ID, "x")
	if err == nil || !strings.Contains(err.Error(), "rename unsupported for antigravity sessions") {
		t.Fatalf("err = %v, want the unsupported-agent error", err)
	}
}

func TestHandleSessionRenameClaudeWithoutModSurfacesTheModError(t *testing.T) {
	// The real claude adapter has no mod connection here.
	d := newNode(map[session.TmuxServer]*tmux.Client{})
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		Agent:          "claude",
		AgentSessionID: "ses_9",
		Status:         session.StatusIdle,
	})
	_, err := callRename(d, s.ID, "Disk name")
	if err == nil || !strings.Contains(err.Error(), "rename needs the Argus plugin connection") {
		t.Fatalf("err = %v, want the mod-unreachable error", err)
	}
}

func TestHandleSessionRenameSurfacesAgentErrors(t *testing.T) {
	fr := &fakeRenamer{err: errors.New("daemon unavailable")}
	d, s := renameTestNode(t, "codex", fr)
	if _, err := callRename(d, s.ID, "x"); err == nil || !strings.Contains(err.Error(), "daemon unavailable") {
		t.Fatalf("err = %v, want the agent error", err)
	}
	if got, _ := d.reg.Get(s.ID); got.Name != "Old" {
		t.Errorf("registry name = %q, want Old (no optimistic update on failure)", got.Name)
	}
}
