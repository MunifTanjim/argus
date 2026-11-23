package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
)

func setWorkdirs(t *testing.T, dir string, workdirs ...string) {
	t.Helper()
	def, err := bot.LoadDefinition(dir)
	if err != nil {
		t.Fatal(err)
	}
	def.Workdirs = workdirs
	if err := def.Save(dir); err != nil {
		t.Fatal(err)
	}
}

func TestBotSpawnAndResumeCarryWorkdirs(t *testing.T) {
	r := newThreadRig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	abs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "code"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(r.bot.Dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	setWorkdirs(t, r.bot.Dir, abs, "~/code", "notes", filepath.Join(abs, "missing"))
	want := []string{abs, filepath.Join(home, "code"), filepath.Join(r.bot.Dir, "notes")}

	var spawned, resumed []string
	spawn, resume := r.d.spawnFn, r.d.resumeFn
	r.d.spawnFn = func(ctx context.Context, p api.SpawnParams, presetID string) (api.SpawnResult, error) {
		spawned = workdirsFrom(ctx)
		return spawn(ctx, p, presetID)
	}
	r.d.resumeFn = func(ctx context.Context, agent, id, cwd, existing string) (string, error) {
		resumed = workdirsFrom(ctx)
		return resume(ctx, agent, id, cwd, existing)
	}
	res, err := r.send(t, api.BotSendParams{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spawned, want) {
		t.Errorf("spawn workdirs = %v, want %v", spawned, want)
	}
	r.end(res.SessionID)
	if err := r.store.EndThreadSession(context.Background(), res.SessionID, "ended"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.send(t, api.BotSendParams{Text: "again"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resumed, want) {
		t.Errorf("resume workdirs = %v, want %v", resumed, want)
	}
}

func TestBotWorkdirsNoneLeavesContext(t *testing.T) {
	r := newThreadRig(t)
	a := bot.Bot{ID: r.bot.ID, Name: r.bot.Name, Dir: r.bot.Dir}
	if dirs := workdirsFrom(r.d.withBotWorkdirs(context.Background(), a)); dirs != nil {
		t.Errorf("dirs = %v", dirs)
	}
}

type dirAdderFake struct{ adapter.Adapter }

func (dirAdderFake) AddDirArgs(dirs []string) []string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = "+" + d
	}
	return out
}

func TestAddDirArgs(t *testing.T) {
	ctx := context.WithValue(context.Background(), workdirsKey{}, []string{"/a", "/b"})
	if got := addDirArgs(ctx, dirAdderFake{}, []string{"--resume", "x"}); !slices.Equal(got, []string{"+/a", "+/b", "--resume", "x"}) {
		t.Errorf("args = %q", got)
	}
	if got := addDirArgs(context.Background(), dirAdderFake{}, []string{"x"}); !slices.Equal(got, []string{"x"}) {
		t.Errorf("no workdirs: %q", got)
	}
	var plain adapter.Adapter
	if got := addDirArgs(ctx, plain, []string{"x"}); !slices.Equal(got, []string{"x"}) {
		t.Errorf("no DirAdder: %q", got)
	}
}
