package pi

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

func completionNames(items []api.PromptCompletion) []string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names
}

func ownedWithCommands(t *testing.T, cmds []map[string]any) (*discoverer, *fakePi, string) {
	t.Helper()
	d := newDiscoverer(registry.New(), nil)
	f := &fakePi{t: t, commands: cmds}
	stubSpawn(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id, err := d.spawnSession(ctx, "/w", adapter.SpawnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d, f, id
}

func TestCompleteOwnedListsBuiltinsThenPiCommands(t *testing.T) {
	d, f, id := ownedWithCommands(t, []map[string]any{
		{"name": "fix-tests", "description": "Fix failing tests", "source": "prompt"},
		{"name": "skill:review", "source": "skill"},
		{"name": "deploy", "description": "Ship it", "source": "extension"},
	})
	got, err := d.completions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"compact", "new", "fix-tests", "skill:review", "deploy"}
	if names := completionNames(got); !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if got[2].Description != "Fix failing tests" || got[3].Description != "" {
		t.Fatalf("descriptions = %q, %q", got[2].Description, got[3].Description)
	}
	if f.call("get_commands") == nil {
		t.Fatal("get_commands not sent")
	}
}

func TestCompleteOwnedDropsBuiltinNames(t *testing.T) {
	d, _, id := ownedWithCommands(t, []map[string]any{
		{"name": "compact", "description": "ext"},
		{"name": "new"},
		{"name": "x"},
	})
	got, err := d.completions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if names := completionNames(got); !reflect.DeepEqual(names, []string{"compact", "new", "x"}) {
		t.Fatalf("names = %v", names)
	}
	if got[0] != adapter.CompactCompletion {
		t.Fatalf("compact = %+v, want Argus's own", got[0])
	}
}

func TestCompletePromptIgnoresOtherTriggers(t *testing.T) {
	got, err := (&piAdapter{}).CompletePrompt(context.Background(), session.Session{}, "@")
	if got != nil || err != nil {
		t.Fatalf("CompletePrompt(@) = %v, %v", got, err)
	}
}

func TestCompleteUnknownSession(t *testing.T) {
	_, err := newDiscoverer(registry.New(), nil).completions(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompleteOwnedFallsBackToBuiltins(t *testing.T) {
	old := commandsTimeout
	commandsTimeout = 50 * time.Millisecond
	t.Cleanup(func() { commandsTimeout = old })
	for _, reply := range []string{"fail", "silent"} {
		t.Run(reply, func(t *testing.T) {
			d, f, id := ownedWithCommands(t, nil)
			f.mu.Lock()
			f.commandsReply = reply
			f.mu.Unlock()
			got, err := d.completions(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if names := completionNames(got); !reflect.DeepEqual(names, []string{"compact", "new"}) {
				t.Fatalf("names = %v, want the built-ins", names)
			}
		})
	}
}

func TestCompleteOmitsTheGateCommand(t *testing.T) {
	d, _, id := ownedWithCommands(t, []map[string]any{{"name": "argus-ping", "description": "Argus gate status"}, {"name": "x"}})
	got, err := d.completions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if names := completionNames(got); !reflect.DeepEqual(names, []string{"compact", "new", "x"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestNewCompletionMatchesCompactCase(t *testing.T) {
	if newCompletion.Description != "start a new session" {
		t.Fatalf("new description = %q", newCompletion.Description)
	}
}
