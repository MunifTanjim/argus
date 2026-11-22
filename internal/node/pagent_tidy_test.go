package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
)

func (r *captureRig) seedMemory(t *testing.T, source string, ops ...memory.Op) {
	t.Helper()
	for i := range ops {
		ops[i].Action, ops[i].File = "add", "MEMORY.md"
	}
	if _, err := r.d.pagentApplyMemory(context.Background(), r.pa(t), r.thread, source, ops, ""); err != nil {
		t.Fatal(err)
	}
}

func (r *captureRig) pa(t *testing.T) pagent.Agent {
	t.Helper()
	a, ok, err := r.store.AgentByID(context.Background(), r.agent.ID)
	if err != nil || !ok {
		t.Fatal("agent not found", err)
	}
	return a
}

func (r *captureRig) lastTrailer(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "-C", r.agent.Dir, "log", "-1", "--format=%s%n%(trailers:key=Argus-Source,valueonly)").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func (r *captureRig) tidyEvents(t *testing.T, kind string) int {
	n := 0
	for _, e := range r.events(t, r.thread) {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestTidyRespectsGuards(t *testing.T) {
	r := newCaptureRig(t)
	r.seedMemory(t, "capture", memory.Op{Text: "likes tea"}, memory.Op{Text: "likes green tea"}, memory.Op{Text: "uses vim"}, memory.Op{Text: "uses neovim"}, memory.Op{Text: "lives in Dhaka"})
	before := r.memoryFile(t, "memory/MEMORY.md")

	// Too many changes: 2 of 5 entries, limit is 1.
	r.ex.reply = `{"ops":[{"action":"remove","match":"likes green tea"},{"action":"remove","match":"uses neovim"}]}`
	if err := r.d.pagentTidyNow(context.Background(), r.pa(t)); err != nil {
		t.Fatal(err)
	}
	if got := r.memoryFile(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("memory changed:\n%s", got)
	}
	if r.tidyEvents(t, evMemoryError) != 1 {
		t.Fatal("want a memory.error event")
	}

	// New words.
	r.ex.reply = `{"ops":[{"action":"update","match":"uses neovim","text":"uses neovim daily"}]}`
	_ = r.d.pagentTidyNow(context.Background(), r.pa(t))
	if got := r.memoryFile(t, "memory/MEMORY.md"); got != before {
		t.Fatalf("memory changed:\n%s", got)
	}

	// A user line is never touched.
	path := filepath.Join(r.agent.Dir, "memory", "MEMORY.md")
	if err := os.WriteFile(path, []byte(before+"- prefers dark mode (2026-01-01)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ex.reply = `{"ops":[{"action":"remove","match":"dark mode"}]}`
	_ = r.d.pagentTidyNow(context.Background(), r.pa(t))
	if !strings.Contains(r.memoryFile(t, "memory/MEMORY.md"), "dark mode") {
		t.Fatal("user line dropped")
	}

	// A valid merge goes through as one tidy commit.
	r.ex.reply = `{"ops":[{"action":"remove","match":"likes tea"}]}`
	if err := r.d.pagentTidyNow(context.Background(), r.pa(t)); err != nil {
		t.Fatal(err)
	}
	if got := r.memoryFile(t, "memory/MEMORY.md"); strings.Contains(got, "likes tea") || !strings.Contains(got, "green tea") {
		t.Fatalf("memory:\n%s", got)
	}
	if got := r.lastTrailer(t); !strings.Contains(got, "tidy") {
		t.Fatalf("last commit %q", got)
	}
	if r.tidyEvents(t, evMemoryChanged) < 2 {
		t.Fatal("want a memory.changed event")
	}
}

func TestTidyOnlyWhenNewEntries(t *testing.T) {
	r := newCaptureRig(t)
	ctx := context.Background()
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil || len(r.ex.prompts) != 0 {
		t.Fatalf("tidy of empty memory: %v %d", err, len(r.ex.prompts))
	}
	r.seedMemory(t, "capture", memory.Op{Text: "likes tea"}, memory.Op{Text: "uses vim"})
	r.ex.reply = `{"ops":[]}`
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil || len(r.ex.prompts) != 1 {
		t.Fatalf("first tidy: %v %d", err, len(r.ex.prompts))
	}
	// No change: skipped until memory changes, even after a restart.
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil || len(r.ex.prompts) != 1 {
		t.Fatalf("unchanged tidy: %v %d", err, len(r.ex.prompts))
	}
	r.d.tidyIdle = nil
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil || len(r.ex.prompts) != 1 {
		t.Fatalf("unchanged tidy after restart: %v %d", err, len(r.ex.prompts))
	}
	r.seedMemory(t, "capture", memory.Op{Text: "owns a dog"})
	r.ex.reply = `{"ops":[{"action":"remove","match":"owns a dog"}]}`
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil {
		t.Fatal(err)
	}
	if len(r.ex.prompts) != 2 || !strings.Contains(r.lastTrailer(t), "tidy") {
		t.Fatalf("second tidy: %d %q", len(r.ex.prompts), r.lastTrailer(t))
	}
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil || len(r.ex.prompts) != 2 {
		t.Fatalf("tidy without new entries: %v %d", err, len(r.ex.prompts))
	}
	r.seedMemory(t, "agent", memory.Op{Text: "owns a cat"})
	if err := r.d.pagentTidy(ctx, r.pa(t), false); err != nil || len(r.ex.prompts) != 3 {
		t.Fatalf("tidy after new entry: %v %d", err, len(r.ex.prompts))
	}
}

func TestTidyRemovesExpired(t *testing.T) {
	r := newCaptureRig(t)
	r.seedMemory(t, "capture",
		memory.Op{Text: "sprint ends soon", Until: "2000-01-01"},
		memory.Op{Text: "old release freeze", Until: "2000-02-01"},
		memory.Op{Text: "likes tea"},
		memory.Op{Text: "uses vim", Until: "2999-01-01"},
	)
	r.ex.reply = `{"ops":[]}`
	if err := r.d.pagentTidy(context.Background(), r.pa(t), false); err != nil {
		t.Fatal(err)
	}
	got := r.memoryFile(t, "memory/MEMORY.md")
	if strings.Contains(got, "sprint") || strings.Contains(got, "freeze") || !strings.Contains(got, "likes tea") || !strings.Contains(got, "uses vim") {
		t.Fatalf("memory:\n%s", got)
	}
	if !strings.Contains(r.lastTrailer(t), "tidy") {
		t.Fatalf("last commit %q", r.lastTrailer(t))
	}
}

func TestTidyDueAtTime(t *testing.T) {
	r := newCaptureRig(t)
	r.seedMemory(t, "capture", memory.Op{Text: "likes tea"}, memory.Op{Text: "sprint", Until: "2000-01-01"})
	r.d.pagentCfg.Defaults.Memory.TidyAt = "03:00"
	r.d.pagentCfg.Defaults.Timezone = "UTC"
	day := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	r.d.pagentNow = func() time.Time { return day.Add(2 * time.Hour) }
	r.ex.reply = `{"ops":[]}`
	r.d.pagentTidyDue(context.Background())
	if len(r.ex.prompts) != 0 {
		t.Fatal("tidied before tidy-at")
	}
	r.d.pagentNow = func() time.Time { return day.Add(5 * time.Hour) }
	r.d.pagentTidyDue(context.Background())
	if !strings.Contains(r.lastTrailer(t), "tidy") {
		t.Fatal("not tidied after tidy-at")
	}
	r.seedMemory(t, "agent", memory.Op{Text: "owns a cat"})
	r.d.pagentTidyDue(context.Background())
	if len(r.ex.prompts) != 0 && strings.Count(strings.Join(r.ex.prompts, ""), "Current MEMORY.md") > 1 {
		t.Fatal("tidied twice in one day")
	}
}

func TestTidyRemovesExpiredUserLine(t *testing.T) {
	r := newCaptureRig(t)
	r.seedMemory(t, "capture", memory.Op{Text: "likes tea"}, memory.Op{Text: "uses vim"})
	path := filepath.Join(r.agent.Dir, "memory", "MEMORY.md")
	cur := r.memoryFile(t, "memory/MEMORY.md")
	hand := cur + "- freeze until friday (2026-01-01) · until 2000-01-01\n- prefers dark mode (2026-01-01) · until 2999-01-01\n"
	if err := os.WriteFile(path, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	r.ex.reply = `{"ops":[]}`
	if err := r.d.pagentTidyNow(context.Background(), r.pa(t)); err != nil {
		t.Fatal(err)
	}
	got := r.memoryFile(t, "memory/MEMORY.md")
	if strings.Contains(got, "freeze") || !strings.Contains(got, "dark mode") {
		t.Fatalf("memory:\n%s", got)
	}
}

func TestTidyOnlyInAutoMode(t *testing.T) {
	r := newCaptureRig(t)
	r.seedMemory(t, "capture", memory.Op{Text: "likes tea"}, memory.Op{Text: "sprint", Until: "2000-01-01"})
	for _, mode := range []string{"review", "off"} {
		r.d.pagentCfg.Defaults.Memory.Mode = mode
		if err := r.d.pagentTidyNow(context.Background(), r.pa(t)); err != nil {
			t.Fatal(err)
		}
		if len(r.ex.prompts) != 0 || !strings.Contains(r.memoryFile(t, "memory/MEMORY.md"), "sprint") {
			t.Fatalf("tidy ran in %s mode", mode)
		}
	}
}
