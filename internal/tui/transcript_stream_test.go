package tui

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode"
	"github.com/MunifTanjim/argus/internal/api"
)

func TestApplyDelta(t *testing.T) {
	have := []claudecode.Chunk{{ID: "0"}, {ID: "1"}}
	// from_index 1 replaces chunk 1 and appends chunk 2
	d := api.TranscriptDelta{FromIndex: 1, Chunks: []claudecode.Chunk{{ID: "1", Text: "grown"}, {ID: "2"}}}
	got := applyDelta(have, d)
	if len(got) != 3 || got[1].Text != "grown" || got[2].ID != "2" {
		t.Fatalf("applyDelta = %+v", got)
	}
}

func TestApplyDeltaFromZeroReplacesAll(t *testing.T) {
	have := []claudecode.Chunk{{ID: "0"}, {ID: "1"}}
	d := api.TranscriptDelta{FromIndex: 0, Chunks: []claudecode.Chunk{{ID: "0"}}}
	got := applyDelta(have, d)
	if len(got) != 1 {
		t.Fatalf("want full replace to 1 chunk, got %d", len(got))
	}
}

func TestNewSubIDUnique(t *testing.T) {
	if a, b := newSubID(), newSubID(); a == b || a == "" {
		t.Fatalf("sub ids not unique/non-empty: %q %q", a, b)
	}
}
