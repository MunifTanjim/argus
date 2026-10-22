package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapters"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/procwalk"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

type chainProc struct {
	pid, ppid int
	args      string
}

func callerNode(procs ...chainProc) *Node {
	table := procwalk.Table{}
	for _, p := range procs {
		table[p.pid] = procwalk.Proc{PID: p.pid, PPID: p.ppid, Args: p.args}
	}
	return &Node{
		reg:         registry.New(),
		adapterList: adapters.All(),
		procTable:   func(context.Context) (procwalk.Table, error) { return table, nil },
	}
}

func (d *Node) addAgentSession(agent, id string) {
	d.reg.ApplyHook(registry.HookUpdate{
		Agent: agent, Server: session.TmuxServerDefault, PaneID: "%" + id,
		AgentSessionID: id, Status: session.StatusIdle,
	})
}

func TestResolveCallerClaude(t *testing.T) {
	d := callerNode(
		chainProc{1, 0, "/sbin/launchd"},
		chainProc{100, 1, "/usr/local/bin/claude"},
		chainProc{110, 100, "zsh"},
		chainProc{120, 110, "argus session list"},
	)
	d.addAgentSession("claude", "c1")
	s, err := d.resolveCaller(t.Context(), api.CallerInfo{PID: 120, Env: map[string]string{
		"CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": "c1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "claude" || s.AgentSessionID != "c1" {
		t.Fatalf("got %s/%s", s.Agent, s.AgentSessionID)
	}
}

func TestResolveCallerNearestAgentWins(t *testing.T) {
	d := callerNode(
		chainProc{1, 0, "launchd"},
		chainProc{50, 1, "opencode serve"},
		chainProc{60, 50, "zsh"},
		chainProc{70, 60, "argus session list"},
	)
	d.addAgentSession("claude", "c1")
	d.addAgentSession("opencode", "ses_1")
	s, err := d.resolveCaller(t.Context(), api.CallerInfo{PID: 70, Env: map[string]string{
		"OPENCODE_SESSION_ID": "ses_1", "CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": "c1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "opencode" {
		t.Fatalf("got agent %s", s.Agent)
	}
}

func TestResolveCallerNestedInsideClaude(t *testing.T) {
	d := callerNode(
		chainProc{1, 0, "launchd"},
		chainProc{100, 1, "claude"},
		chainProc{110, 100, "zsh"},
		chainProc{50, 110, "opencode"},
		chainProc{60, 50, "zsh"},
		chainProc{70, 60, "argus session list"},
	)
	d.addAgentSession("claude", "c1")
	d.addAgentSession("opencode", "ses_1")
	s, err := d.resolveCaller(t.Context(), api.CallerInfo{PID: 70, Env: map[string]string{
		"OPENCODE_SESSION_ID": "ses_1", "CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": "c1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "opencode" {
		t.Fatalf("got agent %s", s.Agent)
	}
}

func TestResolveCallerNoAgentAncestor(t *testing.T) {
	d := callerNode(
		chainProc{1, 0, "launchd"},
		chainProc{10, 1, "zsh"},
		chainProc{20, 10, "argus session list"},
	)
	_, err := d.resolveCaller(t.Context(), api.CallerInfo{PID: 20})
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveCallerMissingVariable(t *testing.T) {
	d := callerNode(
		chainProc{1, 0, "launchd"},
		chainProc{30, 1, "codex"},
		chainProc{40, 30, "argus session list"},
	)
	_, err := d.resolveCaller(t.Context(), api.CallerInfo{PID: 40})
	if err == nil || !strings.Contains(err.Error(), "CODEX_THREAD_ID") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveCallerUnknownSession(t *testing.T) {
	d := callerNode(
		chainProc{1, 0, "launchd"},
		chainProc{100, 1, "claude"},
		chainProc{120, 100, "argus session list"},
	)
	_, err := d.resolveCaller(t.Context(), api.CallerInfo{PID: 120, Env: map[string]string{
		"CLAUDE_PID": "100", "CLAUDE_CODE_SESSION_ID": "missing",
	}})
	if !errors.Is(err, api.ErrNoCaller) {
		t.Fatalf("err = %v", err)
	}
}
