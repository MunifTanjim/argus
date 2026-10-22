package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/session"
)

type sessionFake struct {
	method string
	params any
	result any
}

func (f *sessionFake) call(method string, params, out any) error {
	f.method, f.params = method, params
	switch o := out.(type) {
	case *api.AgentSendResult:
		*o = f.result.(api.AgentSendResult)
	case *api.AgentSpawnResult:
		*o = f.result.(api.AgentSpawnResult)
	case *[]session.Session:
		*o = f.result.([]session.Session)
	}
	return nil
}

func runSession(t *testing.T, f *sessionFake, args ...string) (string, error) {
	dial := func(*cobra.Command) (caller, func(), error) { return f.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(sessionCmd(dial))
	root.SetArgs(append([]string{"session"}, args...))
	var err error
	out := captureStdout(t, func() { err = root.Execute() })
	return out, err
}

func TestCallerEnvNamesIncludeAgentProcessEnv(t *testing.T) {
	names := callerEnvNames()
	for _, want := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_PID", "CODEX_THREAD_ID", "OPENCODE_SESSION_ID"} {
		if !slices.Contains(names, want) {
			t.Errorf("callerEnvNames() = %v; missing %s", names, want)
		}
	}
}

func TestCallerInfoCollectsOnlyKnownVars(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "c1")
	t.Setenv("FOO", "bar")
	for _, name := range callerEnvNames() {
		if name != "CLAUDE_CODE_SESSION_ID" {
			t.Setenv(name, "")
			_ = os.Unsetenv(name)
		}
	}
	info := callerInfo()
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d", info.PID)
	}
	if !maps.Equal(info.Env, map[string]string{"CLAUDE_CODE_SESSION_ID": "c1"}) {
		t.Errorf("Env = %v", info.Env)
	}
}

func TestSessionSendPrintsMessageID(t *testing.T) {
	f := &sessionFake{result: api.AgentSendResult{MessageID: "m-00000001"}}
	out, err := runSession(t, f, "send", "n1:s1", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if out != "m-00000001\n" {
		t.Errorf("out = %q", out)
	}
	p := f.params.(api.AgentSendParams)
	if f.method != api.MethodAgentSend || p.SessionID != "n1:s1" || p.Text != "hello there" {
		t.Errorf("call = %s %+v", f.method, p)
	}
}

func TestSessionSpawnDefaultsCwd(t *testing.T) {
	f := &sessionFake{result: api.AgentSpawnResult{SessionID: "n1:s2", TaskID: "t-1"}}
	out, err := runSession(t, f, "spawn", "--agent", "claude")
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if p := f.params.(api.AgentSpawnParams); p.Cwd != wd || !filepath.IsAbs(p.Cwd) || p.Agent != "claude" {
		t.Errorf("params = %+v", p)
	}
	if out != "n1:s2\ntask t-1\n" {
		t.Errorf("out = %q", out)
	}
}

func TestSessionRespondRejectsMalformedAnswer(t *testing.T) {
	f := &sessionFake{}
	_, stderr := captureOutput(t, func() {
		if _, err := runSession(t, f, "respond", "m-1", "--answer", "no equals"); err == nil {
			t.Error("want error")
		}
	})
	if f.method != "" {
		t.Errorf("called %s", f.method)
	}
	if stderr == "" {
		t.Error("no diagnostic")
	}
}

func TestSessionRespondAnswers(t *testing.T) {
	f := &sessionFake{}
	if _, err := runSession(t, f, "respond", "m-1", "--answer", "Pick?=A", "--answer", "a=b?=B"); err != nil {
		t.Fatal(err)
	}
	p := f.params.(api.AgentRespondParams)
	if p.NoticeID != "m-1" || !maps.Equal(p.Answers, map[string]any{"Pick?": "A", "a=b?": "B"}) {
		t.Errorf("params = %+v", p)
	}
}

func TestSessionRespondAllow(t *testing.T) {
	f := &sessionFake{}
	if _, err := runSession(t, f, "respond", "m-1", "deny", "--reason", "no"); err != nil {
		t.Fatal(err)
	}
	if p := f.params.(api.AgentRespondParams); p.Behavior != "deny" || p.Reason != "no" {
		t.Errorf("params = %+v", p)
	}
}

func TestSessionListTable(t *testing.T) {
	f := &sessionFake{result: []session.Session{
		{ID: "n1:a", Name: "one", Agent: "claude", Status: "idle", Cwd: "/x", Parent: &session.ParentRef{SessionID: "n1:p"}},
		{ID: "n1:b", Name: "two", Agent: "codex", Status: "working", Cwd: "/y", Parent: &session.ParentRef{Gone: true}},
		{ID: "n1:c", Name: "three", Agent: "codex", Status: "working", Cwd: "/z"},
	}}
	out, err := runSession(t, f, "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "PARENT") {
		t.Fatalf("out = %q", out)
	}
	for i, want := range []string{"n1:p", "gone", "-"} {
		fields := strings.Fields(lines[i+1])
		if fields[len(fields)-2] != want {
			t.Errorf("row %d = %q, want parent %q", i, lines[i+1], want)
		}
	}
}
