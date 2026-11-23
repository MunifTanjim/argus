package node

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/botgit"
	"github.com/MunifTanjim/argus/internal/push"
)

func (r *runRig) sink(t *testing.T) *recordSink {
	t.Helper()
	s := &recordSink{}
	sinks := []push.Sink{s}
	r.d.ownerSinks.Store(&sinks)
	return s
}

func waitPushes(s *recordSink, n int) []push.Notification {
	deadline := time.Now().Add(2 * time.Second)
	for len(s.all()) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	return s.all()
}

func TestHealthCLIMissing(t *testing.T) {
	r := newRunRig(t)
	sink := r.sink(t)
	r.d.lookPath = func(string) bool { return false }
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	for i := 0; i < 2; i++ {
		run, _, _ := r.store.ClaimOccurrence(context.Background(), r.a.ID, rt.Slug, time.Unix(int64(1_600_000_000+i), 0), "routine", false)
		r.d.startRun(context.Background(), r.a, rt, run, "")
		r.wait(t)
		if got := r.run(t, run.ID); got.State != bot.RunFailed || !strings.Contains(got.Error, "not installed") {
			t.Fatalf("run: %+v", got)
		}
	}
	if len(r.spawns) != 0 {
		t.Fatal("spawned without the CLI")
	}
	ev := r.chatEvents(t, evHealth)
	if len(ev) != 1 || ev[0]["key"] != healthCLIMissing {
		t.Fatalf("health events: %+v", ev)
	}
	if got := waitPushes(sink, 2); len(got) != 1 || !strings.Contains(got[0].Title, "not installed") {
		t.Fatalf("pushes: %+v", got)
	}
	// Once the CLI is back the notice is resolved, so a recurrence notifies again.
	r.d.lookPath = func(string) bool { return true }
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "no_change"})
	r.wait(t)
	r.d.lookPath = func(string) bool { return false }
	run, _, _ := r.store.ClaimOccurrence(context.Background(), r.a.ID, rt.Slug, time.Unix(1_800_000_000, 0), "routine", false)
	r.d.startRun(context.Background(), r.a, rt, run, "")
	r.wait(t)
	if n := len(r.chatEvents(t, evHealth)); n != 2 {
		t.Fatalf("health events after recurrence = %d", n)
	}
}

func TestHealthSpawnFailures(t *testing.T) {
	r := newRunRig(t)
	r.d.spawnFn = func(context.Context, api.SpawnParams, string) (api.SpawnResult, error) {
		return api.SpawnResult{}, errors.New("tmux exploded")
	}
	for i := 0; i < 3; i++ {
		if _, err := r.send(t, api.BotSendParams{Text: "hi", ThreadID: r.chat}); err == nil {
			t.Fatal("send succeeded")
		}
		if n := len(r.chatEvents(t, evHealth)); (i < 2 && n != 0) || (i == 2 && n != 1) {
			t.Fatalf("after %d failures: %d health events", i+1, n)
		}
	}
	if ev := r.chatEvents(t, evHealth); !strings.Contains(ev[0]["message"].(string), "tmux exploded") {
		t.Fatalf("health: %+v", ev)
	}
}

func TestPausedRoutinePushes(t *testing.T) {
	r := newRunRig(t)
	sink := r.sink(t)
	rt := r.routine(t, "billing", goalBody+"budget: { sessions: 1 }\n")
	c := r.start(t, rt)
	r.report(t, c, reportRunParams{Result: "no_change"})
	r.wait(t)
	got := waitPushes(sink, 1)
	if len(got) != 1 || !strings.Contains(got[0].Title, "routine billing paused (budget)") {
		t.Fatalf("pushes: %+v", got)
	}
	if _, err := callBot[api.BotRoutineInfo](t, r.d.handleBotRoutineResume, api.BotRoutineRef{BotID: r.a.ID, Slug: "billing"}); err != nil {
		t.Fatal(err)
	}
	r.d.notify.mu.Lock()
	_, open := r.d.notify.notes[r.a.ID+"/"+healthPausedPrefix+"billing"]
	r.d.notify.mu.Unlock()
	if open {
		t.Fatal("resume left the paused notice open")
	}
}

func TestUsageByStartedBy(t *testing.T) {
	r := newRunRig(t)
	ctx := context.Background()
	rt := r.routine(t, "inbox", "what: check inbox\nstarts:\n  - every: 1h\n")
	for i := 0; i < 2; i++ {
		c := r.start(t, rt)
		r.report(t, c, reportRunParams{Result: "no_change"})
		r.wait(t)
	}
	runs, _ := r.store.BotRunsSince(ctx, r.a.ID, time.Time{})
	_ = r.store.RecordResult(ctx, runs[0].ID, bot.RunResult{Result: "no_change", Cost: 0.25, Tokens: 100})
	if _, err := r.send(t, api.BotSendParams{Text: "hello", New: true}); err != nil {
		t.Fatal(err)
	}
	res, err := callBot[api.BotUsageResult](t, r.d.handleBotUsage, api.BotUsageParams{BotID: r.a.ID})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]api.BotUsageRow{}
	for _, row := range res.Rows {
		by[row.StartedBy] = row
	}
	if by["routine"].Sessions != 2 || by["routine"].Cost != 0.25 || by["routine"].Tokens != 100 || by["you"].Sessions != 1 || res.Total.Sessions != 3 {
		t.Fatalf("usage: %+v", res)
	}
	res, _ = callBot[api.BotUsageResult](t, r.d.handleBotUsage, api.BotUsageParams{BotID: r.a.ID, Since: time.Now().Add(time.Hour)})
	if res.Total.Sessions != 0 {
		t.Fatalf("usage since the future: %+v", res)
	}
}

func TestBotSet(t *testing.T) {
	r := newRunRig(t)
	set := func(key, value string) error {
		_, err := callBot[api.BotSetResult](t, r.d.handleBotSet, api.BotSetParams{BotID: r.a.ID, Key: key, Value: value})
		return err
	}
	for _, kv := range [][2]string{{"notify", "all"}, {"proactivity", "low"}, {"memory.mode", "review"}, {"quiet-hours", "22:00-07:00"}, {"brief.enabled", "true"}, {"brief.at", "07:30"}} {
		if err := set(kv[0], kv[1]); err != nil {
			t.Fatalf("%s: %v", kv[0], err)
		}
	}
	s := r.d.botSettings(r.a)
	if s.Notify != "all" || s.Proactivity != "low" || s.MemoryMode != "review" || s.QuietHours != "22:00-07:00" || !s.BriefEnabled || s.BriefAt != "07:30" {
		t.Fatalf("settings: %+v", s)
	}
	repo, _ := botgit.Open(context.Background(), r.a.Dir, botgit.ModeNested)
	c, err := repo.Log(context.Background(), []string{bot.DefinitionFile}, 1)
	if err != nil || len(c) != 1 || c[0].Trailers["Argus-Source"] != "you" || c[0].Subject != "bot: set brief.at 07:30" {
		t.Fatalf("commit: %+v %v", c, err)
	}
	for _, kv := range [][2]string{{"notify", "loud"}, {"colour", "red"}, {"quiet-hours", "late"}, {"brief.at", "noon"}} {
		if err := set(kv[0], kv[1]); err == nil {
			t.Errorf("%s=%s accepted", kv[0], kv[1])
		}
	}
	if err := set("notify", ""); err != nil {
		t.Fatal(err)
	}
	if s := r.d.botSettings(r.a); s.Notify != r.d.botCfg.Defaults.Notify {
		t.Fatalf("unset notify: %q", s.Notify)
	}
}

func TestBotInfoSettings(t *testing.T) {
	r := newRunRig(t)
	r.d.botCfg.Defaults.Notify, r.d.botCfg.Defaults.Proactivity, r.d.botCfg.Defaults.Memory.Mode = "needs-you", "default", "auto"
	show := func() map[string]api.BotSetting {
		info, err := callBot[api.BotInfo](t, r.d.handleBotShow, api.BotRef{BotID: r.a.ID})
		if err != nil {
			t.Fatal(err)
		}
		return info.Settings
	}
	got := show()
	if got["notify"] != (api.BotSetting{Value: "needs-you"}) || got["proactivity"] != (api.BotSetting{Value: "default"}) || got["memory_mode"] != (api.BotSetting{Value: "auto"}) {
		t.Fatalf("defaults: %+v", got)
	}
	if _, err := callBot[api.BotSetResult](t, r.d.handleBotSet, api.BotSetParams{BotID: r.a.ID, Key: "notify", Value: "all"}); err != nil {
		t.Fatal(err)
	}
	if got = show(); got["notify"] != (api.BotSetting{Value: "all", Explicit: true}) || got["proactivity"].Explicit {
		t.Fatalf("after set: %+v", got)
	}
}
