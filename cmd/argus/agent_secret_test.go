package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestAgentSecretCommands(t *testing.T) {
	f := twoAgents()
	var set api.PAgentSecretSetParams
	var rm api.PAgentSecretRef
	call := func(method string, params, out any) error {
		switch method {
		case api.MethodPAgentSecretSet:
			set = params.(api.PAgentSecretSetParams)
			return nil
		case api.MethodPAgentSecretList:
			*out.(*api.PAgentSecretListResult) = api.PAgentSecretListResult{Secrets: []api.PAgentSecretInfo{{Name: "GH_TOKEN", Description: "issues"}}}
			return nil
		case api.MethodPAgentSecretRm:
			rm = params.(api.PAgentSecretRef)
			return nil
		}
		return f.call(method, params, out)
	}
	prev := readSecretValue
	readSecretValue = func(string) (string, error) { return "ghp_value", nil }
	t.Cleanup(func() { readSecretValue = prev })
	run := func(args ...string) string {
		t.Helper()
		dial := func(*cobra.Command) (caller, func(), error) { return call, func() {}, nil }
		root := &cobra.Command{Use: "argus"}
		root.AddCommand(agentCmd(dial))
		root.SetArgs(append([]string{"agent", "secret"}, args...))
		var err error
		out, _ := captureOutput(t, func() { err = root.Execute() })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	out := run("set", "scout", "GH_TOKEN", "--description", "issues")
	if set != (api.PAgentSecretSetParams{AgentID: "a1", Name: "GH_TOKEN", Description: "issues", Value: "ghp_value"}) || strings.Contains(out, "ghp_value") {
		t.Errorf("set = %+v out %q", set, out)
	}
	if out := run("list", "scout"); !strings.Contains(out, "GH_TOKEN") || !strings.Contains(out, "issues") {
		t.Errorf("list = %q", out)
	}
	run("rm", "scout", "GH_TOKEN")
	if rm != (api.PAgentSecretRef{AgentID: "a1", Name: "GH_TOKEN"}) {
		t.Errorf("rm = %+v", rm)
	}
}

func TestReadPipedSecretKeepsLines(t *testing.T) {
	for in, want := range map[string]string{
		"token\n":                     "token",
		"token\r\n":                   "token",
		"token":                       "token",
		"-----BEGIN\nabc\n-----END\n": "-----BEGIN\nabc\n-----END",
		"{\n  \"k\": 1\n}\n\n":        "{\n  \"k\": 1\n}\n",
	} {
		got, err := readPipedSecret(strings.NewReader(in))
		if err != nil || got != want {
			t.Errorf("%q = %q %v", in, got, err)
		}
	}
}
