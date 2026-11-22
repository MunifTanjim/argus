package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

type fakeSettingsNode struct {
	agents []api.PAgentInfo
	params map[string]any
}

func (f *fakeSettingsNode) call(method string, params, out any) error {
	if f.params == nil {
		f.params = map[string]any{}
	}
	f.params[method] = params
	switch method {
	case api.MethodPAgentList:
		*out.(*api.PAgentListResult) = api.PAgentListResult{Agents: f.agents}
	case api.MethodPAgentUsage:
		*out.(*api.PAgentUsageResult) = api.PAgentUsageResult{
			AgentID: params.(api.PAgentUsageParams).AgentID,
			Rows:    []api.PAgentUsageRow{{StartedBy: "routine", Sessions: 4, Cost: 1.5, Tokens: 900}, {StartedBy: "you", Sessions: 2}},
			Total:   api.PAgentUsageRow{StartedBy: "total", Sessions: 6, Cost: 1.5, Tokens: 900},
		}
	case api.MethodPAgentSet:
		*out.(*api.PAgentSetResult) = api.PAgentSetResult{Commit: "abc"}
	}
	return nil
}

func execSettings(t *testing.T, f *fakeSettingsNode, args ...string) (string, string, error) {
	t.Helper()
	dial := func(*cobra.Command) (caller, func(), error) { return f.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(agentCmd(dial))
	root.SetArgs(append([]string{"agent"}, args...))
	var err error
	out, errOut := captureOutput(t, func() { err = root.Execute() })
	return out, errOut, err
}

func TestAgentUsageCmd(t *testing.T) {
	f := &fakeSettingsNode{agents: []api.PAgentInfo{{ID: "n1:a1", Name: "scout", Status: "idle"}}}
	out, _, err := execSettings(t, f, "usage", "scout", "--since", "7d")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"routine", "$1.50", "900", "total"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage lacks %q:\n%s", want, out)
		}
	}
	p := f.params[api.MethodPAgentUsage].(api.PAgentUsageParams)
	if p.AgentID != "n1:a1" || time.Since(p.Since) < 7*24*time.Hour-time.Minute || time.Since(p.Since) > 7*24*time.Hour+time.Minute {
		t.Fatalf("params: %+v", p)
	}
	if _, errOut, err := execSettings(t, f, "usage", "--since", "soon"); err == nil || !strings.Contains(errOut, "--since") {
		t.Fatalf("bad --since: %v %q", err, errOut)
	}
}

func TestAgentSetCmd(t *testing.T) {
	f := &fakeSettingsNode{agents: []api.PAgentInfo{{ID: "n1:a1", Name: "scout", Status: "idle"}}}
	out, _, err := execSettings(t, f, "set", "scout", "notify", "all")
	if err != nil || !strings.Contains(out, "notify = all") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if p := f.params[api.MethodPAgentSet].(api.PAgentSetParams); p.AgentID != "n1:a1" || p.Key != "notify" || p.Value != "all" {
		t.Fatalf("params: %+v", p)
	}
	if out, _, err := execSettings(t, f, "set", "scout", "notify", ""); err != nil || !strings.Contains(out, "default") {
		t.Fatalf("unset: %v\n%s", err, out)
	}
}
