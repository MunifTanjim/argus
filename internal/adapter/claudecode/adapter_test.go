package claudecode

import (
	"context"
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
)

func TestSpawnCommandModel(t *testing.T) {
	var a ccAdapter
	name, args := a.SpawnCommand(adapter.SpawnOptions{Prompt: "fix it", Model: "opus[1m]"})
	if name != "claude" || !slices.Equal(args, []string{"--model", "opus[1m]", "fix it"}) {
		t.Fatalf("got %s %q", name, args)
	}
	if _, args := a.SpawnCommand(adapter.SpawnOptions{Prompt: "fix it"}); !slices.Equal(args, []string{"fix it"}) {
		t.Fatalf("default model must add no flag: %q", args)
	}
	if _, args := a.SpawnCommand(adapter.SpawnOptions{Model: "haiku"}); !slices.Equal(args, []string{"--model", "haiku"}) {
		t.Fatalf("no prompt: %q", args)
	}
}

func TestListModelsClaudeAliases(t *testing.T) {
	got, err := ccAdapter{}.ListModels(context.Background())
	want := []api.ModelInfo{
		{ID: "opus", Name: "Opus"}, {ID: "opus[1m]", Name: "Opus (1M)"},
		{ID: "sonnet", Name: "Sonnet"}, {ID: "sonnet[1m]", Name: "Sonnet (1M)"},
		{ID: "haiku", Name: "Haiku"}, {ID: "haiku[1m]", Name: "Haiku (1M)"},
		{ID: "fable", Name: "Fable"}, {ID: "fable[1m]", Name: "Fable (1M)"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
}
