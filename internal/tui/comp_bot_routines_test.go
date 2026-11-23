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

func routinesClient() *botClient {
	cl := botViewClient()
	next := time.Now().Add(2*time.Hour + time.Minute)
	cl.routines = []api.BotRoutineInfo{
		{BotID: "n1:u", Slug: "migrate-billing", What: "goal", State: "active", NextDue: &next,
			Budget: api.BotRoutineBudget{Sessions: 30, Cost: 5}, Used: api.BotRoutineBudget{Sessions: 6, Cost: 2}},
		{BotID: "n1:u", Slug: "morning-brief", Starts: []string{"every day at 08:00"}, State: "paused", PausedReason: "budget"},
	}
	return cl
}

// routineCalls lists the routine actions sent, as "method slug".
func routineCalls(cl *botClient) []string {
	var out []string
	for i, call := range cl.calls {
		switch call {
		case api.MethodBotRoutineRun, api.MethodBotRoutinePause, api.MethodBotRoutineResume:
			out = append(out, call+" "+cl.params[i].(api.BotRoutineRef).Slug)
		}
	}
	return out
}

func TestBotViewRoutines(t *testing.T) {
	cl := routinesClient()
	m := loadedBotView(t, cl)
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
	want := []string{api.MethodBotRoutineRun + " migrate-billing", api.MethodBotRoutinePause + " migrate-billing",
		api.MethodBotRoutineResume + " morning-brief", api.MethodBotRoutineResume + " morning-brief"}
	if got := routineCalls(cl); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("routine calls = %v, want %v", got, want)
	}
}

func TestBotViewRoutineFlashAndRefresh(t *testing.T) {
	cl := routinesClient()
	m := loadedBotView(t, cl)
	m, _ = upd(m, botRoutineMsg{botID: "n1:u", verb: "run", slug: "migrate-billing"})
	if m.flash != "started migrate-billing" {
		t.Errorf("flash = %q", m.flash)
	}
	cl.routines = cl.routines[:1]
	m, _ = upd(m, m.reloadBotViewsCmd()())
	if a := m.baseComp().(botComp); len(a.routines) != 1 {
		t.Fatalf("bot.changed did not reload the routines: %d", len(a.routines))
	}
}

func TestBotMemoryReviewShowsRoutineProposals(t *testing.T) {
	cl := memoryClient()
	cl.props = []api.BotMemoryProposal{{ID: "n1:p9", Source: "bot", Kind: "routine", Slug: "billing",
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
	m, _ = upd(m, keyMsg("u")) // the cursor is on the last entry: routine.changed
	_, cmd := upd(m, keyMsg("y"))
	if cmd == nil {
		t.Fatal("u on a routine change undoes it")
	}
	cmd()
	if p, _ := botParams[api.BotMemoryUndoParams](cl, api.MethodBotMemoryUndo); p != (api.BotMemoryUndoParams{BotID: "n1:u", Commit: "r123", ThreadID: "n1:t1"}) {
		t.Fatalf("undo params = %+v", p)
	}
}

func TestThreadHandoffAnswerShowsBotAndText(t *testing.T) {
	cl := threadClient()
	cl.timeline.Entries = append(cl.timeline.Entries, transcript.Entry{
		ID: "ev:10", Kind: transcript.EntryArgus, Label: "handoff.answered",
		Detail:    "Answer from researcher: Launch is on 3 March.\nSource: the press page.",
		Timestamp: threadT0.Add(6 * time.Minute).Format(time.RFC3339Nano),
		Event: &transcript.ArgusEvent{ID: 10, Kind: "handoff.answered",
			Payload: json.RawMessage(`{"bot":"researcher","text":"Launch is on 3 March.\nSource: the press page."}`)},
	})
	m := openedThread(t, cl)
	out := ansi.Strip(m.View().Content)
	for _, want := range []string{"researcher  answered", "Launch is on 3 March.", "Source: the press page."} {
		if !strings.Contains(out, want) {
			t.Errorf("thread view lacks %q:\n%s", want, out)
		}
	}
}

func TestBotMemoryReviewShowsBotProposals(t *testing.T) {
	cl := memoryClient()
	cl.props = []api.BotMemoryProposal{{ID: "n1:p7", Source: "bot", Kind: "bot",
		Summary: "create bot writer: Drafts release notes", CreatedAt: time.Now()}}
	m := openedMemory(t, cl)
	m = typeKeys(m, "gT")
	if out := ansi.Strip(m.main.top().view(&ctx{m: &m}, 100, 30)); !strings.Contains(out, "create bot writer: Drafts release notes") {
		t.Fatalf("review tab:\n%s", out)
	}
	m, cmd := upd(m, keyMsg("a"))
	cmd()
	if p, _ := botParams[api.BotMemoryDecideParams](cl, api.MethodBotMemoryDecide); p != (api.BotMemoryDecideParams{ProposalID: "n1:p7", Accept: true}) {
		t.Fatalf("decide = %+v", p)
	}
}

func TestBotViewCursorFollowsRow(t *testing.T) {
	cl := routinesClient()
	m := loadedBotView(t, cl)
	m = typeKeys(m, "j") // on migrate-billing's thread (t2)
	// t2 finishes a turn and moves to the top; a new thread arrives.
	ts := botViewThreads()
	cl.threads["n1:u"] = []api.BotThreadInfo{{ID: "n1:t3", BotID: "n1:u", Kind: "chat", Title: "new"}, ts[1], ts[0]}
	m, _ = upd(m, m.loadBotViewCmd("n1:u")())
	m, _ = upd(m, keyMsg("enter"))
	if th, ok := m.main.top().(threadComp); !ok || th.threadID != "n1:t2" {
		t.Fatalf("enter opened %+v, want n1:t2", m.main.top())
	}
	m, _ = upd(m, keyMsg("esc"))

	// On a routine, a new thread does not move R to another routine.
	m = typeKeys(m, "jj") // from t2 past t1 to migrate-billing
	cl.threads["n1:u"] = append([]api.BotThreadInfo{{ID: "n1:t4", BotID: "n1:u", Kind: "chat"}}, cl.threads["n1:u"]...)
	m, _ = upd(m, m.loadBotViewCmd("n1:u")())
	cl.calls, cl.params = nil, nil
	m, cmd := upd(m, keyMsg("R"))
	cmd()
	if got := routineCalls(cl); len(got) != 1 || got[0] != api.MethodBotRoutineRun+" migrate-billing" {
		t.Fatalf("R ran %v", got)
	}
}
