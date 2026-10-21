package antigravity

import (
	"context"
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/session"
)

func TestCompletePromptSlashListsBuiltins(t *testing.T) {
	items, err := agAdapter{}.CompletePrompt(context.Background(), session.Session{}, "/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range items {
		names = append(names, it.Name)
		if it.Description == "" {
			t.Errorf("item %+v: want a description", it)
		}
	}
	slices.Sort(names)
	if want := []string{"clear", "compact", "exit", "new"}; !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestCompletePromptOtherTriggerIsEmpty(t *testing.T) {
	items, err := agAdapter{}.CompletePrompt(context.Background(), session.Session{}, "@")
	if err != nil || len(items) != 0 {
		t.Fatalf("items = %v, err = %v; want none", items, err)
	}
}
