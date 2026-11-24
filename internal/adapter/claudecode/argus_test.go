package claudecode

import (
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

const (
	argusBlock1     = "<argus-message id=\"m-1\" kind=\"task\" from=\"lead\" session=\"home:argus:%1\">\nReview the API\n</argus-message>"
	argusBlock2     = "<argus-message id=\"m-2\" kind=\"text\" from=\"lead\" session=\"home:argus:%1\">\nAlso check the docs\n</argus-message>"
	argusTaskPrompt = "<argus-message id=\"m-1\" kind=\"task\" from=\"lead\" session=\"home:argus:%1\">\nReview the API\n</argus-message>\n\n<argus-message id=\"m-2\" kind=\"text\" from=\"lead\" session=\"home:argus:%1\">\nAlso check the docs\n</argus-message>\n\nDo the task yourself."
)

// wantArgusRows checks es holds the prompt's two messages as two user rows.
func wantArgusRows(t *testing.T, es []transcript.Entry, baseID string) {
	t.Helper()
	if len(es) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(es), es)
	}
	for i, want := range []struct{ text, id, msgID, body string }{
		{argusBlock1, baseID + ".m-1", "m-1", "Review the API"},
		{argusBlock2, baseID + ".m-2", "m-2", "Also check the docs"},
	} {
		e := es[i]
		if e.Kind != transcript.EntryUser || e.Text != want.text || e.ID != want.id {
			t.Errorf("entry %d = %+v", i, e)
		}
		if len(e.ArgusMessages) != 1 || e.ArgusMessages[0].ID != want.msgID || e.ArgusMessages[0].Body != want.body {
			t.Errorf("entry %d messages = %+v", i, e.ArgusMessages)
		}
	}
}

func TestFoldEntriesSplitsArgusMessages(t *testing.T) {
	es := foldEntries([]parser.Chunk{{Type: parser.UserChunk, Timestamp: t0, UserText: argusTaskPrompt}}, nil, nil, "", true)
	wantArgusRows(t, es, "0")
}

func TestFoldEntriesSplitsArgusMessagesInQueuedPrompt(t *testing.T) {
	es := foldEntries([]parser.Chunk{
		{Type: parser.UserChunk, Timestamp: t0, UserText: "go"},
		{Type: parser.AIChunk, Timestamp: t0, Items: []parser.DisplayItem{
			{Type: parser.ItemQueuedPrompt, Text: argusTaskPrompt, Timestamp: t0.Add(time.Minute)},
		}},
	}, nil, nil, "", true)
	if es[0].ArgusMessages != nil {
		t.Fatalf("plain prompt got ArgusMessages %+v", es[0].ArgusMessages)
	}
	wantArgusRows(t, es[1:3], "1.0")
}

func TestSummarizeChunksTaskLineFromArgusTask(t *testing.T) {
	for text, want := range map[string]string{argusTaskPrompt: "Review the API", "fix the bug\nmore": "fix the bug"} {
		s := summarizeChunks([]parser.Chunk{{Type: parser.UserChunk, UserText: text, Timestamp: t0}})
		if s == nil || s.Task != want {
			t.Errorf("Task for %q = %+v; want %q", text, s, want)
		}
	}
}
