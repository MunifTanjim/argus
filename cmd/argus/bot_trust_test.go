package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestBotTrustCmd(t *testing.T) {
	var got api.BotRef
	accepted := []string{".git/hooks/pre-commit (added)"}
	call := func(method string, params, out any) error {
		if np, ok := params.(nodeParams); ok {
			params = np.Params
		}
		switch method {
		case api.MethodBotList:
			*out.(*api.BotListResult) = api.BotListResult{Bots: []api.BotInfo{{ID: "a1", Name: "scout", NodeID: "n1"}}}
		case api.MethodBotTrust:
			got = params.(api.BotRef)
			*out.(*api.BotTrustResult) = api.BotTrustResult{Accepted: accepted}
		}
		return nil
	}
	run := func(args ...string) string {
		t.Helper()
		dial := func(*cobra.Command) (caller, func(), error) { return call, func() {}, nil }
		root := &cobra.Command{Use: "argus"}
		root.AddCommand(botCmd(dial))
		root.SetArgs(append([]string{"bot"}, args...))
		var err error
		out, _ := captureOutput(t, func() { err = root.Execute() })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := run("trust", "scout"); !strings.Contains(out, ".git/hooks/pre-commit (added)") || got.BotID != "a1" {
		t.Fatalf("trust: %+v\n%s", got, out)
	}
	accepted = nil
	if out := run("trust", "scout"); !strings.Contains(out, "nothing had changed") {
		t.Fatalf("trust unchanged:\n%s", out)
	}
}
