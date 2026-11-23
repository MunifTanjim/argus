package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestBotActivityLists(t *testing.T) {
	f := twoBots()
	var got api.BotActivityParams
	call := func(method string, params, out any) error {
		if method == api.MethodBotActivity {
			got = params.(api.BotActivityParams)
			*out.(*api.BotActivityResult) = api.BotActivityResult{Activity: []api.BotActivity{{
				StartedBy: "routine", Tool: "Bash", Target: "git", Class: []string{"outward"},
				Decision: "draft", DecidedBy: "floor", Reason: "staged as a draft", CreatedAt: time.Now(),
			}}}
			return nil
		}
		return f.call(method, params, out)
	}
	dial := func(*cobra.Command) (caller, func(), error) { return call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(botCmd(dial))
	root.SetArgs([]string{"bot", "activity", "scout", "--unattended"})
	var err error
	out, _ := captureOutput(t, func() { err = root.Execute() })
	if err != nil {
		t.Fatal(err)
	}
	if got.BotID != "a1" || !got.Unattended || got.Limit != 50 {
		t.Errorf("params = %+v", got)
	}
	for _, want := range []string{"Bash git [outward]", "draft", "floor", "routine"} {
		if !strings.Contains(out, want) {
			t.Errorf("out lacks %q:\n%s", want, out)
		}
	}
}
