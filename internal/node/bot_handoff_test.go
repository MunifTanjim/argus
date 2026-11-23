package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/routine"
	"github.com/MunifTanjim/argus/internal/db"
	"github.com/MunifTanjim/argus/internal/orch"
	"github.com/MunifTanjim/argus/internal/session"
)

// askRig is a run rig whose bot scout may message a second bot,
// researcher, with coordination on.
type askRig struct {
	*runRig
	researcher bot.Bot
}

func newAskRig(t *testing.T) *askRig {
	t.Helper()
	r := newRunRig(t)
	info, err := callBot[api.BotInfo](t, r.d.handleBotCreate, api.BotCreateParams{Name: "researcher"})
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := r.store.BotByID(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	def, err := bot.LoadDefinition(r.a.Dir)
	if err != nil {
		t.Fatal(err)
	}
	def.CanMessage = []string{"researcher", "ghost"}
	if err := def.Save(r.a.Dir); err != nil {
		t.Fatal(err)
	}
	r.d.botCfg.Coordination.Enabled = true
	r.d.botCfg.Coordination.MaxDepth = 3
	r.d.botCfg.Coordination.MaxRequestsPerRoot = 12
	r.d.botCfg.Coordination.AnswerTimeout = time.Hour
	return &askRig{runRig: r, researcher: res}
}

var askResearcher = askBotParams{Bot: "researcher", Task: "find the launch date", Bounds: "read only", DoneWhen: "a date with a source"}

func (r *askRig) ask(t *testing.T, threadID string, p askBotParams) error {
	t.Helper()
	_, err := r.d.mcpAskBot(context.Background(), r.callerIn(threadID), p)
	return err
}

// threadEvents returns the payloads of a thread's events of kind.
func (r *askRig) threadEvents(t *testing.T, threadID, kind string) []map[string]any {
	t.Helper()
	saved := r.chat
	r.chat = threadID
	defer func() { r.chat = saved }()
	return r.chatEvents(t, kind)
}

// handoffRun returns the run executing a hand-off.
func (r *askRig) handoffRun(t *testing.T) *runCtl {
	t.Helper()
	var c *runCtl
	r.until(t, "hand-off run spawned", func() bool {
		r.d.routineRuns.Range(func(_, v any) bool {
			if rc := v.(*runCtl); rc.handoff != nil {
				if _, sid := rc.ids(); sid != "" {
					c = rc
					return false
				}
			}
			return true
		})
		return c != nil
	})
	return c
}

func TestAskBotRefusals(t *testing.T) {
	r := newAskRig(t)
	r.d.botCfg.Coordination.Enabled = false
	if err := r.ask(t, r.chat, askResearcher); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("coordination off: %v", err)
	}
	r.d.botCfg.Coordination.Enabled = true

	// Unknown and not allowed get the same answer.
	notAllowed := askResearcher
	notAllowed.Bot = "scout2"
	unknown := askResearcher
	unknown.Bot = "ghost"
	errA, errB := r.ask(t, r.chat, notAllowed), r.ask(t, r.chat, unknown)
	if errA == nil || errB == nil || strings.ReplaceAll(errA.Error(), "scout2", "X") != strings.ReplaceAll(errB.Error(), "ghost", "X") {
		t.Fatalf("refusals differ: %v / %v", errA, errB)
	}
	if got := r.threadEvents(t, r.chat, evHandoffRefused); len(got) != 3 || got[2]["bot"] != "ghost" {
		t.Fatalf("refused events = %v", got)
	}
	if r.threadEvents(t, r.chat, evHandoffAsked) != nil {
		t.Fatal("a refused ask was recorded as asked")
	}
}

func TestAskBotDepthAncestorAndRootCap(t *testing.T) {
	r := newAskRig(t)
	ctx := context.Background()
	// scout is answering a hand-off in a run thread: its asks join that chain.
	run := r.runThread(t, "th_run", startedByHandoff)
	parent := bot.Handoff{
		ID: "ho_parent", RootID: "ho_root", Depth: 3, Ancestors: []string{botKey("other", "bot_x")},
		FromBot: "bot_x", FromName: "boss", ToBot: r.a.ID, ToName: "scout", ToThread: run, Request: []byte(`{}`),
	}
	if err := r.store.InsertHandoff(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := r.ask(t, run, askResearcher); err == nil || !strings.Contains(err.Error(), "3 deep") {
		t.Fatalf("depth: %v", err)
	}

	// researcher already in the chain.
	r.store.InsertHandoff(ctx, bot.Handoff{ID: "ho_p2", RootID: "ho_root2", Depth: 1,
		Ancestors: []string{botKey(r.d.id, r.researcher.ID)}, FromBot: r.researcher.ID, ToBot: r.a.ID,
		ToThread: r.runThread(t, "th_run2", startedByHandoff), Request: []byte(`{}`)})
	if err := r.ask(t, "th_run2", askResearcher); err == nil || !strings.Contains(err.Error(), "already part of this chain") {
		t.Fatalf("ancestor: %v", err)
	}

	// The root's requests are used up.
	r.d.botCfg.Coordination.MaxRequestsPerRoot = 1
	r.store.InsertHandoff(ctx, bot.Handoff{ID: "ho_p3", RootID: "ho_root3", Depth: 1,
		Ancestors: []string{botKey("other", "bot_y")}, FromBot: "bot_y", ToBot: r.a.ID,
		ToThread: r.runThread(t, "th_run3", startedByHandoff), Request: []byte(`{}`)})
	if err := r.ask(t, "th_run3", askResearcher); err == nil || !strings.Contains(err.Error(), "used its 1 requests") {
		t.Fatalf("root cap: %v", err)
	}
	if got := r.threadEvents(t, "th_run3", evHandoffRefused); len(got) != 1 {
		t.Fatalf("refusal not recorded in the asking thread: %v", got)
	}
}

func TestAskBotAnswerDelivered(t *testing.T) {
	r := newAskRig(t)
	if err := r.ask(t, r.chat, askResearcher); err != nil {
		t.Fatal(err)
	}
	if got := r.threadEvents(t, r.chat, evHandoffAsked); len(got) != 1 || got[0]["bot"] != "researcher" {
		t.Fatalf("asked events = %v", got)
	}
	c := r.handoffRun(t)
	if c.bot.ID != r.researcher.ID || c.run.StartedBy != startedByHandoff {
		t.Fatalf("run in %s started by %s", c.bot.Name, c.run.StartedBy)
	}
	prompt := r.lastPrompt()
	for _, want := range []string{"The bot scout asked you", "Task: find the launch date", "Bounds: read only", "Done when: a date with a source", "report_run"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	th, sid := c.ids()
	thread, _, _ := r.store.Thread(context.Background(), th)
	if thread.Kind != threadRun || thread.StartedBy != startedByHandoff || !strings.HasPrefix(thread.Title, "scout: find the launch date") {
		t.Fatalf("thread = %+v", thread)
	}
	answer := "Launch is on 3 March.\nSource: the press page."
	if _, err := r.d.mcpReportRun(context.Background(), callerRef{Local: &session.Session{ID: sid, BotID: r.researcher.ID, BotThreadID: th}},
		reportRunParams{Result: "done", Summary: answer}); err != nil {
		t.Fatal(err)
	}
	r.until(t, "answer event", func() bool { return len(r.threadEvents(t, r.chat, evHandoffAnswered)) == 1 })
	ev := r.threadEvents(t, r.chat, evHandoffAnswered)[0]
	if ev["text"] != answer || ev["bot"] != "researcher" || ev["bot_id"] != r.researcher.ID {
		t.Fatalf("answer event = %v", ev)
	}
	// The asking session is gone, so the chat gets a session with the answer.
	r.until(t, "answer delivered", func() bool {
		return strings.HasPrefix(r.lastPrompt(), "Answer from researcher:\n\n"+answer)
	})
	r.wait(t)
	if got := r.run(t, c.run.ID); got.State != bot.RunDone {
		t.Fatalf("run = %+v", got)
	}
	// A run without a routine file leaves no routine state behind.
	if _, ok := r.d.routineState(context.Background(), r.researcher.ID, handoffSlug); ok {
		t.Error("hand-off run left routine state")
	}
}

func TestAskBotNoReportIsNoAnswer(t *testing.T) {
	r := newAskRig(t)
	if err := r.ask(t, r.chat, askResearcher); err != nil {
		t.Fatal(err)
	}
	c := r.handoffRun(t)
	_, sid := c.ids()
	r.end(sid)
	r.until(t, "no-answer event", func() bool { return len(r.threadEvents(t, r.chat, evHandoffNoAnswer)) == 1 })
	if ev := r.threadEvents(t, r.chat, evHandoffNoAnswer)[0]; ev["text"] != reasonNoReport {
		t.Fatalf("no-answer event = %v", ev)
	}
	r.wait(t)
}

func TestAskBotTimeout(t *testing.T) {
	r := newAskRig(t)
	ctx := context.Background()
	old := bot.Handoff{
		ID: "ho_old", RootID: "ho_old", Depth: 1, FromBot: r.a.ID, FromName: "scout", FromThread: r.chat,
		ToBot: r.researcher.ID, ToName: "researcher", Request: []byte(`{}`), CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	fresh := old
	fresh.ID, fresh.RootID, fresh.CreatedAt = "ho_new", "ho_new", time.Now()
	for _, h := range []bot.Handoff{old, fresh} {
		if err := r.store.InsertHandoff(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	r.d.expireHandoffs(ctx, time.Now())
	got := r.threadEvents(t, r.chat, evHandoffNoAnswer)
	if len(got) != 1 || got[0]["handoff_id"] != "ho_old" || got[0]["text"] != "no answer within 1h" {
		t.Fatalf("no-answer events = %v", got)
	}
	r.until(t, "no-answer delivered", func() bool { return r.lastPrompt() == "No answer from researcher: no answer within 1h" })
	// A late answer is dropped.
	r.d.answerAsker(ctx, "ho_old", bot.HandoffAnswered, "done", "late")
	if len(r.threadEvents(t, r.chat, evHandoffAnswered)) != 0 {
		t.Fatal("a late answer was recorded")
	}
	if h, _, _ := r.store.Handoff(ctx, "ho_new"); h.State != bot.HandoffPending {
		t.Fatalf("fresh hand-off ended: %+v", h)
	}
}

func (r *askRig) withOrch(t *testing.T) *orch.Store {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	s := orch.NewStore(sqlDB, time.Now)
	r.d.orch = s
	return s
}

func TestHandoffFromAnotherNode(t *testing.T) {
	r := newAskRig(t)
	store := r.withOrch(t)
	ctx := withPeerNode(context.Background(), "nodeb")
	params, _ := json.Marshal(api.OrchPeerHandoffParams{
		HandoffID: "ho_remote", RootID: "ho_remote", Depth: 1, Ancestors: []string{"nodeb:bot_boss"},
		FromBot: "bot_boss", FromName: "boss", ToBot: r.researcher.ID,
		Request: json.RawMessage(`{"task":"count the files","bounds":"read only","done_when":"a number"}`),
	})
	if _, err := r.d.handleOrchPeerHandoff(ctx, params); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.handleOrchPeerHandoff(ctx, params); err != nil {
		t.Fatal("a repeated push fails:", err)
	}
	c := r.handoffRun(t)
	if !strings.Contains(r.lastPrompt(), "The bot boss asked you") {
		t.Fatalf("prompt = %s", r.lastPrompt())
	}
	th, sid := c.ids()
	if _, err := r.d.mcpReportRun(context.Background(), callerRef{Local: &session.Session{ID: sid, BotID: r.researcher.ID, BotThreadID: th}},
		reportRunParams{Result: "done", Summary: "42 files"}); err != nil {
		t.Fatal(err)
	}
	r.wait(t)
	items, err := store.Pending(context.Background(), "nodeb", 10)
	if err != nil {
		t.Fatal(err)
	}
	var answer api.OrchPeerHandoffAnswerParams
	if len(items) != 1 || items[0].Method != api.MethodOrchPeerHandoffAnswer || json.Unmarshal(items[0].Params, &answer) != nil {
		t.Fatalf("outbox = %+v", items)
	}
	if answer != (api.OrchPeerHandoffAnswerParams{HandoffID: "ho_remote", State: bot.HandoffAnswered, Result: "done", Text: "42 files"}) {
		t.Fatalf("answer = %+v", answer)
	}
}

func TestHandoffAnswerFromAnotherNode(t *testing.T) {
	r := newAskRig(t)
	r.withOrch(t)
	if err := r.store.InsertHandoff(context.Background(), bot.Handoff{
		ID: "ho_out", RootID: "ho_out", Depth: 1, FromBot: r.a.ID, FromName: "scout", FromThread: r.chat,
		ToNode: "nodeb", ToBot: "bot_far", ToName: "far", Request: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(api.OrchPeerHandoffAnswerParams{HandoffID: "ho_out", State: bot.HandoffAnswered, Result: "done", Text: "here"})
	if _, err := r.d.handleOrchPeerHandoffAnswer(withPeerNode(context.Background(), "nodec"), params); err == nil {
		t.Fatal("an answer from another node was taken")
	}
	if _, err := r.d.handleOrchPeerHandoffAnswer(withPeerNode(context.Background(), "nodeb"), params); err != nil {
		t.Fatal(err)
	}
	got := r.threadEvents(t, r.chat, evHandoffAnswered)
	if len(got) != 1 || got[0]["text"] != "here" || got[0]["bot"] != "far" || got[0]["node"] != "nodeb" {
		t.Fatalf("answer events = %v", got)
	}
}

func TestChatAskAfterAnswerJoinsChain(t *testing.T) {
	r := newAskRig(t)
	saved := handoffStart
	handoffStart = func(*Node, context.Context, bot.Bot, routine.Routine, bot.RoutineRun, bot.Handoff) {}
	t.Cleanup(func() { handoffStart = saved })
	r.d.botCfg.Coordination.MaxRequestsPerRoot = 2
	ctx := context.Background()
	answerLast := func() string {
		t.Helper()
		asked := r.threadEvents(t, r.chat, evHandoffAsked)
		id := asked[len(asked)-1]["handoff_id"].(string)
		r.d.answerAsker(ctx, id, bot.HandoffAnswered, "progress", "a date")
		return id
	}
	if err := r.ask(t, r.chat, askResearcher); err != nil {
		t.Fatal(err)
	}
	first := answerLast()
	// Asking again on the turn the answer started stays in the first chain.
	if err := r.ask(t, r.chat, askResearcher); err != nil {
		t.Fatal(err)
	}
	second := answerLast()
	if h, _, _ := r.store.Handoff(ctx, second); h.RootID != first {
		t.Fatalf("follow-up root = %q, want %q", h.RootID, first)
	}
	if err := r.ask(t, r.chat, askResearcher); err == nil || !strings.Contains(err.Error(), "used its 2 requests") {
		t.Fatalf("root cap: %v", err)
	}
	// The user's next message starts a new chain.
	r.d.chatChains.Delete(r.chat)
	if err := r.ask(t, r.chat, askResearcher); err != nil {
		t.Fatalf("after user message: %v", err)
	}
}
