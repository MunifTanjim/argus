package node

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
)

func TestBriefScheduledAndPrimaryOnly(t *testing.T) {
	r := newSchedRig(t)
	ctx := context.Background()
	r.d.pagentCfg.Defaults.Brief.Enabled = true
	r.d.pagentCfg.Defaults.Brief.At = "09:00"
	r.tick()
	st, ok := r.state(t, briefSlug)
	if !ok || !st.NextDueAt.Equal(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("brief state: %+v %v", st, ok)
	}
	r.at(23 * time.Hour)
	r.tick()
	if r.startCount() != 1 || r.starts[0].Slug != briefSlug {
		t.Fatalf("starts: %+v", r.starts)
	}

	// With a primary agent, only the primary writes briefs.
	other := pagent.Agent{ID: pagent.NewID(), Name: "other", Dir: t.TempDir(), CreatedAt: schedT0}
	if err := r.store.InsertAgent(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.d.pagentPrimaryFile, []byte(r.agent.ID), 0o644); err != nil {
		t.Fatal(err)
	}
	r.tick()
	if sts, _ := r.store.RoutineStates(ctx, other.ID); len(sts) != 0 {
		t.Fatalf("a non-primary agent got a brief: %+v", sts)
	}
	// The dial off turns briefs off.
	r.d.pagentCfg.Defaults.Proactivity = "off"
	if r.d.briefWanted(ctx, r.agent, r.d.pagentSettings(r.agent)) {
		t.Fatal("brief wanted with proactivity off")
	}
}

func TestBriefRunDelivers(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	r.d.pagentCfg.Defaults.Brief.Enabled = true
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	goal := r.routine(t, "billing", goalBody)
	_ = goal
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "needs_you", Summary: "log in to the mail site"})
	r.wait(t)
	r.d.pagentHealth(ctx, r.a, r.chat, "disk", "the disk is nearly full", "")

	f, err := briefRoutine(r.d.pagentSettings(r.a))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.store.UpsertRoutineState(ctx, pagent.RoutineState{AgentID: r.a.ID, Slug: briefSlug, Enabled: true, AnchorAt: time.Now()})
	b := r.start(t, f.r)
	p := r.lastPrompt()
	for _, want := range []string{"## Daily brief", "### Agent scout", "inbox needs_you: log in to the mail site", "the disk is nearly full", "billing: old client deleted", "Run routine _brief now."} {
		if !strings.Contains(p, want) {
			t.Errorf("brief prompt lacks %q:\n%s", want, p)
		}
	}
	r.report(t, b, reportRunParams{Result: "progress", Summary: "Morning. The mail site needs your login."})
	r.wait(t)
	ev := r.chatEvents(t, evBrief)
	if len(ev) != 1 || ev[0]["text"] != "Morning. The mail site needs your login." {
		t.Fatalf("brief events: %+v", ev)
	}
	if n := len(r.chatEvents(t, evRoutineResult)); n != 0 {
		t.Fatalf("a brief also posted a run result: %d", n)
	}

	b = r.start(t, f.r)
	r.report(t, b, reportRunParams{Result: "no_change", Summary: ""})
	r.wait(t)
	if n := len(r.chatEvents(t, evBrief)); n != 1 {
		t.Fatalf("no_change sent a brief: %d", n)
	}
}

func TestDialCheckBackAndOfferCaps(t *testing.T) {
	set := pagent.Settings{}
	for dial, want := range map[string]time.Duration{"": 24 * time.Hour, "default": 24 * time.Hour, "low": 48 * time.Hour, "high": 12 * time.Hour, "off": 7 * 24 * time.Hour} {
		set.Proactivity = dial
		if got := clampCheckBack(set, 0); got != want {
			t.Errorf("dial %q default wait = %s, want %s", dial, got, want)
		}
		if got := clampCheckBack(set, 3*time.Hour); got != 3*time.Hour {
			t.Errorf("dial %q explicit wait = %s", dial, got)
		}
	}
	set.Proactivity, set.Limits.Limits.MinInterval, set.Limits.Limits.CheckBackMin = "high", time.Hour, 15*time.Minute
	if got := clampCheckBack(set, time.Minute); got != time.Hour {
		t.Errorf("below min-interval: %s", got)
	}
	for dial, want := range map[string]int{"off": 0, "low": 1, "": 3, "default": 3, "high": 6} {
		if got := offerCap(pagent.Settings{Proactivity: dial}); got != want {
			t.Errorf("offer cap %q = %d, want %d", dial, got, want)
		}
	}

	r := newRunRig(t)
	ctx := context.Background()
	offer := offerParams{Title: "Tidy the inbox", Details: "120 unread", Prompt: "Please tidy my inbox."}
	r.d.pagentCfg.Defaults.Proactivity = "low"
	if _, err := r.d.mcpOffer(ctx, r.callerIn(r.chat), offer); err != nil {
		t.Fatal(err)
	}
	if _, err := r.d.mcpOffer(ctx, r.callerIn(r.chat), offer); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("second offer with dial low: %v", err)
	}
	r.d.pagentCfg.Defaults.Proactivity = "default"
	for i := 0; i < 2; i++ {
		if _, err := r.d.mcpOffer(ctx, r.callerIn(r.chat), offer); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.d.mcpOffer(ctx, r.callerIn(r.chat), offer); err == nil {
		t.Fatal("fourth offer with dial default")
	}
	r.d.pagentCfg.Defaults.Proactivity = "off"
	if _, err := r.d.mcpOffer(ctx, r.callerIn(r.chat), offer); err == nil || !strings.Contains(err.Error(), "off") {
		t.Fatalf("offer with dial off: %v", err)
	}
	r.d.pagentCfg.Defaults.Proactivity = "high"
	if _, err := r.d.mcpOffer(ctx, r.callerIn(r.runThread(t, "th_rt", "routine")), offer); err == nil {
		t.Fatal("offer from a routine run")
	}
	if _, err := r.d.mcpOffer(ctx, r.callerIn(r.runThread(t, "th_cb", "check-back")), offer); err != nil {
		t.Fatalf("offer from a check-back run: %v", err)
	}
	if n := len(r.chatEvents(t, evOffer)); n != 4 {
		t.Fatalf("offer events in the chat = %d", n)
	}
}

func TestOfferStartAndDismiss(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	offer := offerParams{Title: "Tidy the inbox", Prompt: "Please tidy my inbox."}
	res, err := r.d.mcpOffer(ctx, r.callerIn(r.chat), offer)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]string)["offer_id"]
	r.mu.Lock()
	before := len(r.spawns) + len(r.delivered)
	r.mu.Unlock()
	if len(r.spawns)+len(r.delivered) != before {
		t.Fatal("an offer started work by itself")
	}
	out, err := callPAgent[api.PAgentSendResult](t, r.d.handlePAgentOfferStart, api.PAgentOfferParams{ThreadID: r.chat, OfferID: id})
	if err != nil || out.ThreadID != r.chat {
		t.Fatalf("start: %+v %v", out, err)
	}
	r.mu.Lock()
	sent := len(r.spawns) == 1 && r.spawns[0].p.Prompt == "Please tidy my inbox."
	r.mu.Unlock()
	if !sent {
		t.Fatalf("prompt not sent: %+v", r.spawns)
	}
	if _, err := callPAgent[api.PAgentSendResult](t, r.d.handlePAgentOfferStart, api.PAgentOfferParams{ThreadID: r.chat, OfferID: id}); err == nil {
		t.Fatal("started twice")
	}
	res, _ = r.d.mcpOffer(ctx, r.callerIn(r.chat), offerParams{Title: "Other", Prompt: "Do other."})
	other := res.(map[string]string)["offer_id"]
	if _, err := callPAgent[struct{}](t, r.d.handlePAgentOfferDismiss, api.PAgentOfferParams{ThreadID: r.chat, OfferID: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := callPAgent[api.PAgentSendResult](t, r.d.handlePAgentOfferStart, api.PAgentOfferParams{ThreadID: r.chat, OfferID: other}); err == nil {
		t.Fatal("dismissed offer started")
	}
	var kinds []string
	for _, ev := range r.events(t, r.chat) {
		if strings.HasPrefix(ev.Kind, "offer") {
			var m map[string]any
			_ = json.Unmarshal(ev.Payload, &m)
			kinds = append(kinds, ev.Kind)
		}
	}
	if strings.Join(kinds, ",") != "offer,offer.started,offer,offer.dismissed" {
		t.Fatalf("offer events: %v", kinds)
	}
}

func TestPausedBriefResumes(t *testing.T) {
	r := newSchedRig(t)
	ctx := context.Background()
	r.d.pagentCfg.Defaults.Brief.Enabled = true
	r.tick()
	if err := r.store.SetRoutineEnabled(ctx, r.agent.ID, briefSlug, false, pausedFailures); err != nil {
		t.Fatal(err)
	}
	info, err := callPAgent[api.PAgentRoutineInfo](t, r.d.handlePAgentRoutineResume, api.PAgentRoutineRef{AgentID: r.agent.ID, Slug: briefSlug})
	if err != nil || info.State != "active" {
		t.Fatalf("resume: %+v %v", info, err)
	}
	// Turning briefs off and on again drops a pause.
	if err := r.store.SetRoutineEnabled(ctx, r.agent.ID, briefSlug, false, pausedAway); err != nil {
		t.Fatal(err)
	}
	r.d.pagentCfg.Defaults.Brief.Enabled = false
	r.tick()
	if _, ok := r.state(t, briefSlug); ok {
		t.Fatal("brief state kept while briefs are off")
	}
	if _, err := callPAgent[api.PAgentRoutineInfo](t, r.d.handlePAgentRoutineResume, api.PAgentRoutineRef{AgentID: r.agent.ID, Slug: briefSlug}); err == nil {
		t.Fatal("resumed a brief that is off")
	}
	r.d.pagentCfg.Defaults.Brief.Enabled = true
	r.tick()
	if st, ok := r.state(t, briefSlug); !ok || !st.Enabled {
		t.Fatalf("brief after turning on: %+v %v", st, ok)
	}
}
