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
	case *api.OrchSendResult:
		*o = f.result.(api.OrchSendResult)
	case *api.OrchSpawnResult:
		*o = f.result.(api.OrchSpawnResult)
	case *api.OrchKillResult:
		*o = f.result.(api.OrchKillResult)
	case *api.SessionRenameResult:
		*o = f.result.(api.SessionRenameResult)
	case *api.OrchListResult:
		*o = f.result.(api.OrchListResult)
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
	f := &sessionFake{result: api.OrchSendResult{MessageID: "m-00000001"}}
	out, err := runSession(t, f, "send", "n1:s1", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if out != "m-00000001\n" {
		t.Errorf("out = %q", out)
	}
	p := f.params.(api.OrchSendParams)
	if f.method != api.MethodOrchMessageSend || p.SessionID != "n1:s1" || p.Text != "hello there" {
		t.Errorf("call = %s %+v", f.method, p)
	}
}

func TestSessionSpawnDefaultsCwd(t *testing.T) {
	f := &sessionFake{result: api.OrchSpawnResult{SessionID: "n1:s2", TaskID: "t-1"}}
	out, err := runSession(t, f, "spawn", "--agent", "claude")
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if p := f.params.(api.OrchSpawnParams); p.Cwd != wd || !filepath.IsAbs(p.Cwd) || p.Agent != "claude" {
		t.Errorf("params = %+v", p)
	}
	if out != "n1:s2\ntask t-1\n" {
		t.Errorf("out = %q", out)
	}
}

func TestSessionSendAndSpawnPassRequestID(t *testing.T) {
	f := &sessionFake{result: api.OrchSendResult{MessageID: "m-00000001"}}
	if _, err := runSession(t, f, "send", "n1:s1", "hi", "--request-id", "r1"); err != nil {
		t.Fatal(err)
	}
	if p := f.params.(api.OrchSendParams); p.RequestID != "r1" {
		t.Errorf("send params = %+v", p)
	}
	f = &sessionFake{result: api.OrchSpawnResult{SessionID: "n1:s2"}}
	if _, err := runSession(t, f, "spawn", "--agent", "claude", "--request-id", "r2"); err != nil {
		t.Fatal(err)
	}
	if p := f.params.(api.OrchSpawnParams); p.RequestID != "r2" {
		t.Errorf("spawn params = %+v", p)
	}
}

func TestSessionKillPrintsKilledSessions(t *testing.T) {
	f := &sessionFake{result: api.OrchKillResult{Killed: []string{"n1:s3", "n1:s2"}}}
	out, err := runSession(t, f, "kill", "n1:s2")
	if err != nil {
		t.Fatal(err)
	}
	if out != "n1:s3\nn1:s2\n" || f.method != api.MethodOrchSessionKill {
		t.Errorf("out = %q method = %s", out, f.method)
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
	p := f.params.(api.OrchRespondParams)
	if p.MessageID != "m-1" || !maps.Equal(p.Answers, map[string]any{"Pick?": "A", "a=b?": "B"}) {
		t.Errorf("params = %+v", p)
	}
}

func TestSessionRespondAllow(t *testing.T) {
	f := &sessionFake{}
	if _, err := runSession(t, f, "respond", "m-1", "deny", "--reason", "no"); err != nil {
		t.Fatal(err)
	}
	if p := f.params.(api.OrchRespondParams); p.Behavior != "deny" || p.Reason != "no" {
		t.Errorf("params = %+v", p)
	}
}

func TestSessionListTable(t *testing.T) {
	f := &sessionFake{result: api.OrchListResult{Sessions: []session.Session{
		{ID: "n1:a", Name: "one", Agent: "claude", Status: "idle", Cwd: "/x", Parent: &session.ParentRef{SessionID: "n1:p"}},
		{ID: "n1:b", Name: "two", Agent: "codex", Status: "working", Cwd: "/y", Parent: &session.ParentRef{Gone: true}},
		{ID: "n1:c", Name: "three", Agent: "codex", Status: "working", Cwd: "/z"},
	}}}
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

func TestSessionListAsksForAllNodes(t *testing.T) {
	f := &sessionFake{result: api.OrchListResult{}}
	if _, err := runSession(t, f, "list"); err != nil {
		t.Fatal(err)
	}
	if p, _ := f.params.(api.OrchListParams); !p.All {
		t.Errorf("params = %+v", f.params)
	}
}

func TestSessionListJSONPrintsResult(t *testing.T) {
	f := &sessionFake{result: api.OrchListResult{
		Sessions:    []session.Session{{ID: "n1:a"}},
		Unreachable: []string{"n2"},
	}}
	out, err := runSession(t, f, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"unreachable"`) || !strings.Contains(out, `"n2"`) || !strings.Contains(out, `"sessions"`) {
		t.Fatalf("out = %q", out)
	}
}

func TestSessionListWarnsOnUnreachable(t *testing.T) {
	f := &sessionFake{result: api.OrchListResult{Unreachable: []string{"n2", "n3"}}}
	var err error
	_, stderr := captureOutput(t, func() { _, err = runSession(t, f, "list") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "n2, n3") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestSessionRenamePrintsName(t *testing.T) {
	f := &sessionFake{result: api.SessionRenameResult{Name: "Review API", Applied: true}}
	out, err := runSession(t, f, "rename", "n1:s2", "Review API")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := f.params.(api.OrchRenameParams)
	if out != "Review API\n" || f.method != api.MethodOrchSessionRename || p.SessionID != "n1:s2" || p.Name != "Review API" {
		t.Errorf("out = %q method = %s params = %+v", out, f.method, f.params)
	}
	f = &sessionFake{result: api.SessionRenameResult{Name: "Later", Applied: false}}
	if out, err = runSession(t, f, "rename", "n1:s2", "Later"); err != nil || out != "Later (the agent applies it later)\n" {
		t.Errorf("pending out = %q err = %v", out, err)
	}
}

func TestSessionSpawnNodeFlag(t *testing.T) {
	f := &sessionFake{result: api.OrchSpawnResult{SessionID: "b:s2"}}
	if _, err := runSession(t, f, "spawn", "--agent", "claude", "--node", "b"); err != nil {
		t.Fatal(err)
	}
	if p := f.params.(api.OrchSpawnParams); p.Node != "b" {
		t.Errorf("spawn params = %+v", p)
	}
}

func TestSessionKillWarnsOnNotKilled(t *testing.T) {
	f := &sessionFake{result: api.OrchKillResult{Killed: []string{"n1:s2"}, NotKilled: []string{"n2:s9"}}}
	var err error
	_, stderr := captureOutput(t, func() { _, err = runSession(t, f, "kill", "n1:s2") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "n2:s9") {
		t.Fatalf("stderr = %q", stderr)
	}
}
