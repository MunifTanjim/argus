package routine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSkipsUnderscore(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "daily.yaml"), []byte("what: x\nstarts:\n  - every: 1h\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "_draft.yaml"), []byte("what: y\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("what: [\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o644)
	got, errs := Load(dir)
	if len(got) != 1 || got[0].Slug != "daily" {
		t.Fatalf("got %+v", got)
	}
	if len(errs) != 1 {
		t.Fatalf("errs %v", errs)
	}
	r := got[0]
	if !r.Enabled || r.Does != "act" || r.Overlap != "skip" || r.Timezone != "auto" || r.ReportTo != "thread" {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if rs, errs := Load(filepath.Join(dir, "missing")); rs != nil || errs != nil {
		t.Fatal("missing dir should be empty")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := defaults()
	in.Slug, in.What = "a", "w"
	in.Starts = []Start{{Schedule: "0 9 * * MON-FRI"}, {At: "2026-12-01T09:00"}}
	in.Budget = Budget{Sessions: 3, Cost: 1.5}
	in.Enabled = false
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	got, errs := Load(dir)
	if len(errs) != 0 || len(got) != 1 {
		t.Fatal(got, errs)
	}
	if got[0].Enabled || got[0].Budget != in.Budget || len(got[0].Starts) != 2 || got[0].Starts[1].At != in.Starts[1].At {
		t.Fatalf("round trip: %+v", got[0])
	}
}

func valid() Routine {
	r := defaults()
	r.Slug, r.What = "a", "w"
	r.Starts = []Start{{Every: "1h"}}
	return r
}

func TestValidateMinInterval(t *testing.T) {
	lim := Limits{MinInterval: 15 * time.Minute}
	if err := Validate(valid(), lim); err != nil {
		t.Fatal(err)
	}
	cases := []Start{{Every: "5m"}, {Schedule: "*/5 * * * *"}, {Schedule: "0,10 * * * *"}, {Schedule: "* * * * *"}}
	for _, s := range cases {
		r := valid()
		r.Starts = []Start{s}
		if Validate(r, lim) == nil {
			t.Errorf("%+v should be refused", s)
		}
	}
	r := valid()
	r.Starts = []Start{{Schedule: "*/15 * * * *"}}
	if err := Validate(r, lim); err != nil {
		t.Errorf("15m ok: %v", err)
	}
}

func TestValidateEnums(t *testing.T) {
	lim := Limits{MinInterval: time.Minute}
	mut := []func(*Routine){
		func(r *Routine) { r.Does = "x" },
		func(r *Routine) { r.Until = "x" },
		func(r *Routine) { r.Until = "outcome" },
		func(r *Routine) { r.IfUnavailable = "x" },
		func(r *Routine) { r.ReportTo = "x" },
		func(r *Routine) { r.Overlap = "x" },
		func(r *Routine) { r.Timezone = "Mars/Base" },
		func(r *Routine) { r.What = "" },
		func(r *Routine) { r.Starts = nil },
		func(r *Routine) { r.Starts = []Start{{Every: "1h", Event: "webhook"}} },
		func(r *Routine) { r.Starts = []Start{{At: "nope"}} },
		func(r *Routine) { r.Starts = []Start{{CheckBack: "x"}} },
		func(r *Routine) { r.Starts = []Start{{Schedule: "bogus"}} },
	}
	for i, m := range mut {
		r := valid()
		m(&r)
		if Validate(r, lim) == nil {
			t.Errorf("case %d should fail", i)
		}
	}
	r := valid()
	r.Starts = []Start{{CheckBack: "self-paced"}, {Event: "webhook"}}
	if err := Validate(r, lim); err != nil {
		t.Error(err)
	}
}

func TestNextDueCronTZ(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	r := valid()
	r.Starts = []Start{{Schedule: "0 9 * * MON-FRI"}}
	after := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC) // Fri 10:00 NY
	got, ok := NextDue(r, after, time.Time{}, ny)
	want := time.Date(2026, 11, 23, 9, 0, 0, 0, ny) // Monday
	if !ok || !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got.UTC().Hour() != 14 {
		t.Fatalf("expected 14:00 UTC, got %v", got.UTC())
	}
	utc, _ := NextDue(r, after, time.Time{}, time.UTC)
	if !utc.Equal(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("utc got %v", utc)
	}
}

func TestNextDueEveryAligned(t *testing.T) {
	anchor := time.Date(2026, 1, 1, 10, 7, 0, 0, time.UTC)
	r := valid()
	r.Starts = []Start{{Every: "2h"}}
	got, ok := NextDue(r, time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC), anchor, nil)
	if !ok || !got.Equal(time.Date(2026, 1, 1, 14, 7, 0, 0, time.UTC)) {
		t.Fatalf("got %v", got)
	}
	got, _ = NextDue(r, anchor.Add(2*time.Hour), anchor, nil)
	if !got.Equal(anchor.Add(4 * time.Hour)) {
		t.Fatalf("strictly after: %v", got)
	}
	got, _ = NextDue(r, anchor.Add(-time.Hour), anchor, nil)
	if !got.Equal(anchor.Add(2 * time.Hour)) {
		t.Fatalf("before anchor: %v", got)
	}
	due := DueBetween(r, anchor, anchor.Add(7*time.Hour), anchor, nil)
	if len(due) != 3 {
		t.Fatalf("due %v", due)
	}
}

func TestNextDueAtOnce(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	r := valid()
	r.Starts = []Start{{At: "2026-12-01T09:00"}, {CheckBack: "self-paced"}}
	before := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	got, ok := NextDue(r, before, time.Time{}, ny)
	if !ok || !got.Equal(time.Date(2026, 12, 1, 9, 0, 0, 0, ny)) {
		t.Fatalf("got %v", got)
	}
	if _, ok := NextDue(r, got, time.Time{}, ny); ok {
		t.Fatal("one-shot must not fire twice")
	}
	r.Starts = []Start{{Event: "webhook"}}
	if _, ok := NextDue(r, before, time.Time{}, ny); ok {
		t.Fatal("event has no schedule")
	}
}

func TestDueBetweenCron(t *testing.T) {
	r := valid()
	r.Starts = []Start{{Schedule: "0 * * * *"}, {Every: "90m"}}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	due := DueBetween(r, from, from.Add(3*time.Hour), from, time.UTC)
	// hourly 1h,2h,3h plus 90m: 1h,1.5h,2h,3h distinct
	if len(due) != 4 || !due[0].Equal(from.Add(time.Hour)) {
		t.Fatalf("due %v", due)
	}
}

func TestDescribeCommon(t *testing.T) {
	cases := map[Start]string{
		{Schedule: "*/15 * * * *"}:    "every 15 minutes",
		{Schedule: "0 */2 * * *"}:     "every 2 hours",
		{Schedule: "30 7 * * *"}:      "daily at 07:30",
		{Schedule: "@daily"}:          "daily at 00:00",
		{Schedule: "0 9 * * MON-FRI"}: "weekdays at 09:00",
		{Schedule: "0 9 * * 1-5"}:     "weekdays at 09:00",
		{Schedule: "0 18 * * Mon"}:    "weekly on Mon at 18:00",
		{Schedule: "0 8 1 * *"}:       "monthly on day 1 at 08:00",
		{Schedule: "5 4 * 6 *"}:       "cron: 5 4 * 6 *",
		{Schedule: "0 9 1,15 * *"}:    "cron: 0 9 1,15 * *",
		{Every: "2h"}:                 "every 2 hours",
		{Every: "1h"}:                 "every hour",
		{Every: "45m"}:                "every 45 minutes",
		{Every: "2d"}:                 "every 2 days",
		{At: "2026-12-01T09:00"}:      "once at 2026-12-01T09:00",
		{CheckBack: "self-paced"}:     "self-paced check-back",
		{Event: "webhook"}:            "on webhook",
	}
	for in, want := range cases {
		if got := Describe(in); got != want {
			t.Errorf("%+v: got %q want %q", in, got, want)
		}
	}
}

func TestParseDurationDays(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"7d":    7 * 24 * time.Hour,
		"1d12h": 36 * time.Hour,
		"1.5d":  36 * time.Hour,
		"90m":   90 * time.Minute,
		"0d":    0,
	} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("%q: got %v, %v want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "d", "xd", "1d-", "1w"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
	r := Routine{What: "x", Does: "act", Until: "stopped", IfUnavailable: "report", ReportTo: "thread", Overlap: "skip", Starts: []Start{{Every: "1d"}}}
	if err := Validate(r, Limits{MinInterval: 15 * time.Minute}); err != nil {
		t.Fatalf("every 1d: %v", err)
	}
}
