package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

type fakeSettingsNode struct {
	bots   []api.BotInfo
	params map[string]any
}

func (f *fakeSettingsNode) call(method string, params, out any) error {
	if f.params == nil {
		f.params = map[string]any{}
	}
	f.params[method] = params
	switch method {
	case api.MethodBotList:
		*out.(*api.BotListResult) = api.BotListResult{Bots: f.bots}
	case api.MethodBotUsage:
		*out.(*api.BotUsageResult) = api.BotUsageResult{
			BotID: params.(api.BotUsageParams).BotID,
			Rows:  []api.BotUsageRow{{StartedBy: "routine", Sessions: 4, Cost: 1.5, Tokens: 900}, {StartedBy: "you", Sessions: 2}},
			Total: api.BotUsageRow{StartedBy: "total", Sessions: 6, Cost: 1.5, Tokens: 900},
		}
	case api.MethodBotSet:
		*out.(*api.BotSetResult) = api.BotSetResult{Commit: "abc"}
	}
	return nil
}

func execSettings(t *testing.T, f *fakeSettingsNode, args ...string) (string, string, error) {
	t.Helper()
	dial := func(*cobra.Command) (caller, func(), error) { return f.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(botCmd(dial))
	root.SetArgs(append([]string{"bot"}, args...))
	var err error
	out, errOut := captureOutput(t, func() { err = root.Execute() })
	return out, errOut, err
}

func TestBotUsageCmd(t *testing.T) {
	f := &fakeSettingsNode{bots: []api.BotInfo{{ID: "n1:a1", Name: "scout", Status: "idle"}}}
	out, _, err := execSettings(t, f, "usage", "scout", "--since", "7d")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"routine", "$1.50", "900", "total"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage lacks %q:\n%s", want, out)
		}
	}
	p := f.params[api.MethodBotUsage].(api.BotUsageParams)
	if p.BotID != "n1:a1" || time.Since(p.Since) < 7*24*time.Hour-time.Minute || time.Since(p.Since) > 7*24*time.Hour+time.Minute {
		t.Fatalf("params: %+v", p)
	}
	if _, errOut, err := execSettings(t, f, "usage", "--since", "soon"); err == nil || !strings.Contains(errOut, "--since") {
		t.Fatalf("bad --since: %v %q", err, errOut)
	}
}

func TestBotSetCmd(t *testing.T) {
	f := &fakeSettingsNode{bots: []api.BotInfo{{ID: "n1:a1", Name: "scout", Status: "idle"}}}
	out, _, err := execSettings(t, f, "set", "scout", "notify", "all")
	if err != nil || !strings.Contains(out, "notify = all") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	if p := f.params[api.MethodBotSet].(api.BotSetParams); p.BotID != "n1:a1" || p.Key != "notify" || p.Value != "all" {
		t.Fatalf("params: %+v", p)
	}
	if out, _, err := execSettings(t, f, "set", "scout", "notify", ""); err != nil || !strings.Contains(out, "default") {
		t.Fatalf("unset: %v\n%s", err, out)
	}
}
