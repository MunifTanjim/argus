package claudecode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/modbridge"
	"github.com/MunifTanjim/argus/internal/session"
)

func TestCompletePromptSlashFromMod(t *testing.T) {
	a, _ := newWiredAdapter(t)
	b := a.ModBridge()
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, []string{modbridge.KindCommands}) {
			b.Ack(c.ID, "", true, json.RawMessage(`[{"name":"compact","description":"Free up context"},{"name":"brainstorming","description":"x"}]`))
		}
	}()
	waitUntil(t, func() bool { return b.Reachable("s1") })

	items, err := a.CompletePrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "/")
	if err != nil {
		t.Fatal(err)
	}
	want := []api.PromptCompletion{
		{Name: "compact", Description: "Free up context"},
		{Name: "brainstorming", Description: "x"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %v, want %v", items, want)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Fatalf("items = %v, want %v", items, want)
		}
	}
}

// A mod that predates the commands kind must not be handed one.
func TestCompletePromptOldModIsEmpty(t *testing.T) {
	a, _ := newWiredAdapter(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan []modbridge.Command, 1)
	go func() { got <- a.ModBridge().Wait(ctx, "s1", time.Minute, nil) }()
	waitUntil(t, func() bool { return a.ModBridge().Reachable("s1") })

	items, err := a.CompletePrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "/")
	if err != nil || len(items) != 0 {
		t.Fatalf("CompletePrompt = (%v, %v), want ([], nil)", items, err)
	}
	cancel()
	if cmds := <-got; len(cmds) != 0 {
		t.Fatalf("poll got %v, want none", cmds)
	}
}

func TestCompletePromptUnreachableIsEmpty(t *testing.T) {
	a, _ := newWiredAdapter(t)
	items, err := a.CompletePrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "/")
	if err != nil || len(items) != 0 {
		t.Fatalf("CompletePrompt = (%v, %v), want ([], nil)", items, err)
	}
}

func TestCompletePromptOtherTriggerIsEmpty(t *testing.T) {
	a, _ := newWiredAdapter(t)
	items, err := a.CompletePrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "$")
	if err != nil || len(items) != 0 {
		t.Fatalf("CompletePrompt = (%v, %v), want ([], nil)", items, err)
	}
}

// The mod that re-polls after one listing commands may be an older one.
func TestCompletePromptRepollWithoutKindIsEmpty(t *testing.T) {
	old := modGrace
	modGrace = time.Second
	t.Cleanup(func() { modGrace = old })
	a, _ := newWiredAdapter(t)
	b := a.ModBridge()
	b.Wait(context.Background(), "s1", time.Millisecond, []string{modbridge.KindCommands})

	type result struct {
		items []api.PromptCompletion
		err   error
	}
	got := make(chan result, 1)
	go func() {
		items, err := a.CompletePrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "/")
		got <- result{items, err}
	}()
	time.Sleep(10 * time.Millisecond)
	polled := make(chan []modbridge.Command, 1)
	go func() { polled <- b.Wait(context.Background(), "s1", 50*time.Millisecond, nil) }()

	if r := <-got; r.err != nil || len(r.items) != 0 {
		t.Fatalf("CompletePrompt = (%v, %v), want ([], nil)", r.items, r.err)
	}
	if cmds := <-polled; len(cmds) != 0 {
		t.Fatalf("poll got %v, want none", cmds)
	}
}

func TestCompletePromptEmptyAckIsEmpty(t *testing.T) {
	a, _ := newWiredAdapter(t)
	b := a.ModBridge()
	go func() {
		for _, c := range b.Wait(context.Background(), "s1", time.Minute, []string{modbridge.KindCommands}) {
			b.Ack(c.ID, "", true, nil)
		}
	}()
	waitUntil(t, func() bool { return b.Reachable("s1") })

	items, err := a.CompletePrompt(context.Background(), session.Session{ID: "x", AgentSessionID: "s1"}, "/")
	if err != nil || len(items) != 0 {
		t.Fatalf("CompletePrompt = (%v, %v), want ([], nil)", items, err)
	}
}
