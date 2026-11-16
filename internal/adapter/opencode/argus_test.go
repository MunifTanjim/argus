package opencode

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
)

func TestFoldMessagesSplitsArgusMessages(t *testing.T) {
	wantArgusRows(t, foldMessages([]ocMessage{ocUser("m1", "<argus-message id=\"m-1\" kind=\"task\" from=\"lead\" session=\"home:argus:%1\">\nReview the API\n</argus-message>\n\n<argus-message id=\"m-2\" kind=\"text\" from=\"lead\" session=\"home:argus:%1\">\nAlso check the docs\n</argus-message>\n\nDo the task yourself.")}, false))
}

func wantArgusRows(t *testing.T, es []transcript.Entry) {
	t.Helper()
	if len(es) < 2 {
		t.Fatalf("got %d entries, want 2 user rows: %+v", len(es), es)
	}
	for i, want := range []struct{ text, id, body string }{
		{"<argus-message id=\"m-1\" kind=\"task\" from=\"lead\" session=\"home:argus:%1\">\nReview the API\n</argus-message>", "m1.m-1", "Review the API"},
		{"<argus-message id=\"m-2\" kind=\"text\" from=\"lead\" session=\"home:argus:%1\">\nAlso check the docs\n</argus-message>", "m1.m-2", "Also check the docs"},
	} {
		e := es[i]
		if e.Kind != transcript.EntryUser || e.Text != want.text || e.ID != want.id {
			t.Errorf("entry %d = %+v", i, e)
		}
		if len(e.ArgusMessages) != 1 || e.ArgusMessages[0].Body != want.body {
			t.Errorf("entry %d messages = %+v", i, e.ArgusMessages)
		}
	}
}
