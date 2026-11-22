package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

func routinesClient() *pagentClient {
	cl := agentViewClient()
	next := time.Now().Add(2*time.Hour + time.Minute)
	cl.routines = []api.PAgentRoutineInfo{
		{AgentID: "n1:u", Slug: "migrate-billing", What: "goal", State: "active", NextDue: &next,
			Budget: api.PAgentRoutineBudget{Sessions: 30, Cost: 5}, Used: api.PAgentRoutineBudget{Sessions: 6, Cost: 2}},
		{AgentID: "n1:u", Slug: "morning-brief", Starts: []string{"every day at 08:00"}, State: "paused", PausedReason: "budget"},
	}
	return cl
}

// routineCalls lists the routine actions sent, as "method slug".
func routineCalls(cl *pagentClient) []string {
	var out []string
	for i, call := range cl.calls {
		switch call {
		case api.MethodPAgentRoutineRun, api.MethodPAgentRoutinePause, api.MethodPAgentRoutineResume:
			out = append(out, call+" "+cl.params[i].(api.PAgentRoutineRef).Slug)
		}
	}
	return out
}

func TestAgentViewRoutines(t *testing.T) {
	cl := routinesClient()
	m := loadedAgentView(t, cl)
	out := ansi.Strip(m.baseComp().view(&ctx{m: &m}, 100, 30))
	for _, want := range []string{"Routines", "migrate-billing  goal · next in 2h · 6/30 sessions · $2/$5",
		"morning-brief  every day at 08:00 · paused: budget"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no routines") {
		t.Errorf("placeholder shown with routines:\n%s", out)
	}

	// Off a routine the routine keys say so.
	if mm, cmd := upd(m, keyMsg("R")); cmd != nil || mm.flash != "pick a routine" {
		t.Fatalf("R on a thread: flash %q", mm.flash)
	}
	m = typeKeys(m, "jj") // past the two threads to migrate-billing
	m, cmd := upd(m, keyMsg("R"))
	m, _ = upd(m, cmd())
	m, cmd = typeKeysCmd(m, "zp")
	cmd()
	m = typeKeys(m, "j")
	m, cmd = upd(m, cmdMsg("routine resume"))
	cmd()
	m, cmd = typeKeysCmd(m, "zp") // morning-brief is paused: zp resumes it
	cmd()
	want := []string{api.MethodPAgentRoutineRun + " migrate-billing", api.MethodPAgentRoutinePause + " migrate-billing",
		api.MethodPAgentRoutineResume + " morning-brief", api.MethodPAgentRoutineResume + " morning-brief"}
	if got := routineCalls(cl); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("routine calls = %v, want %v", got, want)
	}
}

func TestAgentViewRoutineFlashAndRefresh(t *testing.T) {
	cl := routinesClient()
	m := loadedAgentView(t, cl)
	m, _ = upd(m, agentRoutineMsg{agentID: "n1:u", verb: "run", slug: "migrate-billing"})
	if m.flash != "started migrate-billing" {
		t.Errorf("flash = %q", m.flash)
	}
	cl.routines = cl.routines[:1]
	m, _ = upd(m, m.reloadAgentViewsCmd()())
	if a := m.baseComp().(agentComp); len(a.routines) != 1 {
		t.Fatalf("pagent.changed did not reload the routines: %d", len(a.routines))
	}
}

func TestAgentMemoryReviewShowsRoutineProposals(t *testing.T) {
	cl := memoryClient()
	cl.props = []api.PAgentMemoryProposal{{ID: "n1:p9", Source: "agent", Kind: "routine", Slug: "billing",
		Summary: "change goal billing: outcome, verify", CreatedAt: time.Now()}}
	m := openedMemory(t, cl)
	m = typeKeys(m, "gT")
	if out := ansi.Strip(m.main.top().view(&ctx{m: &m}, 100, 30)); !strings.Contains(out, "routine billing: change goal billing: outcome, verify") {
		t.Fatalf("review tab:\n%s", out)
	}
}

func TestThreadRoutineEventText(t *testing.T) {
	ev := func(kind, payload string) transcript.Entry {
		return transcript.Entry{Kind: transcript.EntryArgus, Label: kind, Event: &transcript.ArgusEvent{Kind: kind, Payload: json.RawMessage(payload)}}
	}
	for _, c := range []struct {
		e    transcript.Entry
		want string
	}{
		{ev("routine.result", `{"routine":"brief","result":"done","summary":"sent the brief"}`), "Routine brief done: sent the brief"},
		{ev("routine.changed", `{"routine":"brief","action":"created"}`), "Routine brief created"},
		{ev("routine.proposed", `{"routine":"brief","reason":"run earlier"}`), "Routine brief change to review: run earlier"},
		{ev("routine.error", `{"routine":"brief","message":"bad cron"}`), "Routine brief error: bad cron"},
		{ev("routine.missed", `{"routine":"brief","count":3}`), "Routine brief missed 3 runs"},
		{ev("routine.skipped", `{"routine":"brief","reason":"overlap"}`), "Routine brief skipped a run: overlap"},
		{ev("routine.paused", `{"reason":"away","routines":["a","b"]}`), "Paused a, b: away"},
		{ev("draft", `{"tool":"Bash","command":"git push"}`), "Drafted Bash: git push"},
		{ev("memory.changed", `{"commit":"x"}`), ""},
	} {
		if got := argusEventText(c.e); got != c.want {
			t.Errorf("%s: %q, want %q", c.e.Event.Kind, got, c.want)
		}
	}
	m := openedThread(t, threadClient())
	th := m.main.top().(threadComp)
	th.tr.transcript.entries = append(th.tr.transcript.entries, ev("routine.stale", `{"routine":"brief","due":"2026-11-22T10:00:00Z"}`))
	m.main = m.main.replaceTop(th)
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "Routine brief is overdue") {
		t.Errorf("thread view lacks the routine event:\n%s", out)
	}
}
