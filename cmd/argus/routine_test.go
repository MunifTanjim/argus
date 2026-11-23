package main

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
)

type fakeRoutineNode struct {
	bots   []api.BotInfo
	calls  []string
	params map[string]any
}

func (f *fakeRoutineNode) call(method string, params, out any) error {
	f.calls = append(f.calls, method)
	if f.params == nil {
		f.params = map[string]any{}
	}
	f.params[method] = params
	next := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	switch method {
	case api.MethodBotList:
		*out.(*api.BotListResult) = api.BotListResult{Bots: f.bots}
	case api.MethodBotRoutineList:
		*out.(*api.BotRoutineListResult) = api.BotRoutineListResult{Routines: []api.BotRoutineInfo{{
			BotID: params.(api.BotRef).BotID, Slug: "inbox", What: "check inbox", Starts: []string{"every 2 hours"},
			State: "active", NextDue: &next, Budget: api.BotRoutineBudget{Sessions: 5}, Used: api.BotRoutineBudget{Sessions: 2, Cost: 0.5},
		}}}
	case api.MethodBotRoutineRun:
		*out.(*api.BotRoutineRunResult) = api.BotRoutineRunResult{RunID: "rr1"}
	case api.MethodBotRoutinePause:
		*out.(*api.BotRoutineInfo) = api.BotRoutineInfo{Slug: "inbox", State: "paused", PausedReason: "you"}
	case api.MethodBotRoutineResume:
		*out.(*api.BotRoutineInfo) = api.BotRoutineInfo{Slug: "inbox", State: "active"}
	case api.MethodBotRoutineRuns:
		*out.(*api.BotRoutineRunsResult) = api.BotRoutineRunsResult{Runs: []api.BotRoutineRunInfo{
			{ID: "rr1", StartedBy: "you", Test: true, State: "done", Result: "progress", Summary: "moved invoices", StartedAt: next},
		}}
	case api.MethodBotRoutineHookSecret:
		*out.(*api.BotRoutineHookSecretResult) = api.BotRoutineHookSecretResult{Secret: "whs_abc", Path: "/hooks/scout/inbox"}
	}
	return nil
}

func execRoutine(t *testing.T, f *fakeRoutineNode, args ...string) (string, error) {
	out, _, err := execRoutineErr(t, f, args...)
	return out, err
}

func execRoutineErr(t *testing.T, f *fakeRoutineNode, args ...string) (string, string, error) {
	t.Helper()
	dial := func(*cobra.Command) (caller, func(), error) { return f.call, func() {}, nil }
	root := &cobra.Command{Use: "argus"}
	root.AddCommand(routineCmd(dial))
	root.SetArgs(append([]string{"routine"}, args...))
	var err error
	out, errOut := captureOutput(t, func() { err = root.Execute() })
	return out, errOut, err
}

func routineNode() *fakeRoutineNode {
	return &fakeRoutineNode{bots: []api.BotInfo{{ID: "n1:a1", Name: "scout", Status: "idle"}, {ID: "n1:a2", Name: "other", Status: "idle"}}}
}

func TestRoutineList(t *testing.T) {
	f := routineNode()
	out, err := execRoutine(t, f, "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"scout/inbox", "other/inbox", "every 2 hours", "2/5", "$0.50"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	f = routineNode()
	out, err = execRoutine(t, f, "list", "scout", "--json")
	if err != nil || !strings.Contains(out, `"slug": "inbox"`) || strings.Contains(out, "n1:a2") {
		t.Fatalf("list scout --json: %v\n%s", err, out)
	}
}

func TestRoutineShow(t *testing.T) {
	out, err := execRoutine(t, routineNode(), "show", "scout/inbox")
	if err != nil || !strings.Contains(out, "check inbox") || !strings.Contains(out, "every 2 hours") {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if _, err := execRoutine(t, routineNode(), "show", "scout/nope"); err == nil {
		t.Fatal("unknown routine shown")
	}
	if _, errOut, err := execRoutineErr(t, routineNode(), "show", "scout"); err == nil || !strings.Contains(errOut, "<bot>/<slug>") {
		t.Fatalf("bad name: %v %q", err, errOut)
	}
}

func TestRoutineActions(t *testing.T) {
	f := routineNode()
	out, err := execRoutine(t, f, "run", "scout/inbox")
	if err != nil || !strings.Contains(out, "rr1") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if p := f.params[api.MethodBotRoutineRun].(api.BotRoutineRef); p.BotID != "n1:a1" || p.Slug != "inbox" {
		t.Fatalf("run params: %+v", p)
	}
	if out, err := execRoutine(t, f, "pause", "scout/inbox"); err != nil || !strings.Contains(out, "paused (you)") {
		t.Fatalf("pause: %v\n%s", err, out)
	}
	if out, err := execRoutine(t, f, "resume", "scout/inbox"); err != nil || !strings.Contains(out, "active") {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	out, err = execRoutine(t, f, "runs", "scout/inbox", "--limit", "5")
	if err != nil || !strings.Contains(out, "you (test)") || !strings.Contains(out, "moved invoices") {
		t.Fatalf("runs: %v\n%s", err, out)
	}
	if p := f.params[api.MethodBotRoutineRuns].(api.BotRoutineRunsParams); p.Limit != 5 {
		t.Fatalf("runs params: %+v", p)
	}
	out, err = execRoutine(t, f, "hook-secret", "scout/inbox")
	if err != nil || !strings.Contains(out, "whs_abc") || !strings.Contains(out, "/hooks/scout/inbox") || !strings.Contains(out, "webhooks are off") {
		t.Fatalf("hook-secret: %v\n%s", err, out)
	}
}
