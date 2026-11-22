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

func TestThreadRoutineEventsUseNodeTextAndUndo(t *testing.T) {
	cl := threadClient()
	cl.timeline.Entries = append(cl.timeline.Entries,
		transcript.Entry{ID: "ev:8", Kind: transcript.EntryArgus, Label: "routine.missed", Detail: "Routine brief missed 3 runs",
			Timestamp: threadT0.Add(4 * time.Minute).Format(time.RFC3339Nano),
			Event:     &transcript.ArgusEvent{ID: 8, Kind: "routine.missed", Payload: json.RawMessage(`{"routine":"brief","count":3}`)}},
		transcript.Entry{ID: "ev:9", Kind: transcript.EntryArgus, Label: "routine.changed", Detail: "Routine brief created",
			Timestamp: threadT0.Add(5 * time.Minute).Format(time.RFC3339Nano),
			Event:     &transcript.ArgusEvent{ID: 9, Kind: "routine.changed", Payload: json.RawMessage(`{"routine":"brief","action":"created","commit":"r123"}`)}},
	)
	m := openedThread(t, cl)
	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Routine brief missed 3 runs") || !strings.Contains(out, "Routine brief created  u undo") {
		t.Fatalf("thread view lacks the node's routine text:\n%s", out)
	}
	if strings.Contains(out, "Routine brief missed 3 runs  u undo") {
		t.Error("a missed run offers undo")
	}
	m, cmd := upd(m, keyMsg("u")) // the cursor is on the last entry: routine.changed
	if cmd == nil {
		t.Fatal("u on a routine change undoes it")
	}
	cmd()
	if p, _ := pagentParams[api.PAgentMemoryUndoParams](cl, api.MethodPAgentMemoryUndo); p != (api.PAgentMemoryUndoParams{AgentID: "n1:u", Commit: "r123"}) {
		t.Fatalf("undo params = %+v", p)
	}
}

func TestThreadHandoffAnswerShowsAgentAndText(t *testing.T) {
	cl := threadClient()
	cl.timeline.Entries = append(cl.timeline.Entries, transcript.Entry{
		ID: "ev:10", Kind: transcript.EntryArgus, Label: "handoff.answered",
		Detail:    "Answer from researcher: Launch is on 3 March.\nSource: the press page.",
		Timestamp: threadT0.Add(6 * time.Minute).Format(time.RFC3339Nano),
		Event: &transcript.ArgusEvent{ID: 10, Kind: "handoff.answered",
			Payload: json.RawMessage(`{"agent":"researcher","text":"Launch is on 3 March.\nSource: the press page."}`)},
	})
	m := openedThread(t, cl)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"researcher  answered", "Launch is on 3 March.", "Source: the press page."} {
		if !strings.Contains(out, want) {
			t.Errorf("thread view lacks %q:\n%s", want, out)
		}
	}
}

func TestAgentMemoryReviewShowsAgentProposals(t *testing.T) {
	cl := memoryClient()
	cl.props = []api.PAgentMemoryProposal{{ID: "n1:p7", Source: "agent", Kind: "agent",
		Summary: "create agent writer: Drafts release notes", CreatedAt: time.Now()}}
	m := openedMemory(t, cl)
	m = typeKeys(m, "gT")
	if out := ansi.Strip(m.main.top().view(&ctx{m: &m}, 100, 30)); !strings.Contains(out, "create agent writer: Drafts release notes") {
		t.Fatalf("review tab:\n%s", out)
	}
	m, cmd := upd(m, keyMsg("a"))
	cmd()
	if p, _ := pagentParams[api.PAgentMemoryDecideParams](cl, api.MethodPAgentMemoryDecide); p != (api.PAgentMemoryDecideParams{ProposalID: "n1:p7", Accept: true}) {
		t.Fatalf("decide = %+v", p)
	}
}
