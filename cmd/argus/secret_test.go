package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestSecretGetPrintsValueOnly(t *testing.T) {
	var got api.BotSecretGetParams
	call := func(method string, params, out any) error {
		if method != api.MethodBotSecretGet {
			t.Fatalf("method = %s", method)
		}
		got = params.(api.BotSecretGetParams)
		if got.Name == "NOPE" {
			return errors.New("no secret NOPE for this bot")
		}
		*out.(*api.BotSecretGetResult) = api.BotSecretGetResult{Value: "ghp_value"}
		return nil
	}
	run := func(args ...string) (string, string, error) {
		dial := func(*cobra.Command) (caller, func(), error) { return call, func() {}, nil }
		root := &cobra.Command{Use: "argus"}
		root.AddCommand(secretCmd(dial))
		root.SetArgs(append([]string{"secret", "get"}, args...))
		var err error
		out, errOut := captureOutput(t, func() { err = root.Execute() })
		return out, errOut, err
	}
	out, _, err := run("GH_TOKEN")
	if err != nil || out != "ghp_value" {
		t.Fatalf("out = %q err %v", out, err)
	}
	if got.Name != "GH_TOKEN" || got.Caller.PID != os.Getpid() {
		t.Errorf("params = %+v", got)
	}
	out, errOut, err := run("NOPE")
	if err == nil || out != "" || !strings.Contains(errOut, "no secret NOPE for this bot") {
		t.Errorf("missing: out %q stderr %q err %v", out, errOut, err)
	}
}
