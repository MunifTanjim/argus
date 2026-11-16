package transcript

import (
	"reflect"
	"testing"
)

func TestParseArgusMessagesReadsEachBlockAndDropsFooter(t *testing.T) {
	text := `<argus-message id="m-1a2b3c4d" kind="reply" from="worker-api" session="home:argus:%5" reply-to="m-00000001">
All tests pass.
</argus-message>

<argus-message id="m-5e6f7a8b" kind="interaction" from="argus" child="worker-db" child-session="home:argus:%7">
worker-db (home:argus:%7) asks for permission:
Input: {
  "cmd": "rm -rf build"
}
</argus-message>

Reply to a message with the argus ` + "`reply`" + ` tool (message_id is the message's id), or: argus session reply <id> "<answer>"`
	want := []ArgusMessage{
		{ID: "m-1a2b3c4d", Kind: "reply", From: "worker-api", Session: "home:argus:%5", ReplyTo: "m-00000001", Body: "All tests pass."},
		{ID: "m-5e6f7a8b", Kind: "interaction", From: "argus", Child: "worker-db", ChildSession: "home:argus:%7",
			Body: "worker-db (home:argus:%7) asks for permission:\nInput: {\n  \"cmd\": \"rm -rf build\"\n}"},
	}
	if got := ParseArgusMessages(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseArgusMessagesNeedsLeadingBlock(t *testing.T) {
	for _, text := range []string{
		"see <argus-message id=\"m-1\" kind=\"text\">\nx\n</argus-message>",
		"fix the bug",
		"",
	} {
		if got := ParseArgusMessages(text); got != nil {
			t.Errorf("ParseArgusMessages(%q) = %+v, want nil", text, got)
		}
	}
}

func TestParseArgusMessagesUnescapes(t *testing.T) {
	text := "<argus-message id=\"m-1\" kind=\"text\" from=\"a &amp; b &quot;c&quot; &lt;d&gt;\">\na &lt; b &amp; c&lt;/argus-message>d\n</argus-message>"
	got := ParseArgusMessages(text)
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].From != `a & b "c" <d>` {
		t.Errorf("From = %q", got[0].From)
	}
	if got[0].Body != "a &lt; b &amp; c</argus-message>d" {
		t.Errorf("Body = %q", got[0].Body)
	}
}

func TestArgusTaskLine(t *testing.T) {
	task := "<argus-message id=\"m-1\" kind=\"task\" from=\"lead\" session=\"home:argus:%1\">\n\n  Review the API\nmore\n</argus-message>\n\nDo the task yourself."
	if line, ok := ArgusTaskLine(task); !ok || line != "Review the API" {
		t.Errorf("ArgusTaskLine(task) = %q, %v", line, ok)
	}
	if line, ok := ArgusTaskLine("fix the bug"); ok || line != "" {
		t.Errorf("ArgusTaskLine(plain) = %q, %v", line, ok)
	}
}

func TestUserEntriesSplitsEachMessage(t *testing.T) {
	block1 := "<argus-message id=\"m-1\" kind=\"reply\" from=\"a\" session=\"home:argus:%1\">\nfirst\n</argus-message>"
	block2 := "<argus-message id=\"m-2\" kind=\"interaction\" from=\"argus\" child=\"b\">\nInput: {\n  \"x\": 1\n}\n</argus-message>"
	e := Entry{ID: "3", Kind: EntryUser, Timestamp: "ts", Text: block1 + "\n\n" + block2 + "\n\nReply to a message with …"}
	got := UserEntries(e)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(got), got)
	}
	for i, want := range []struct{ id, text, msg string }{{"3.m-1", block1, "m-1"}, {"3.m-2", block2, "m-2"}} {
		g := got[i]
		if g.ID != want.id || g.Kind != EntryUser || g.Timestamp != "ts" || g.Text != want.text {
			t.Errorf("entry %d = %+v", i, g)
		}
		if len(g.ArgusMessages) != 1 || g.ArgusMessages[0].ID != want.msg {
			t.Errorf("entry %d messages = %+v", i, g.ArgusMessages)
		}
	}
}

func TestUserEntriesKeepsOtherEntries(t *testing.T) {
	plain := Entry{ID: "1", Kind: EntryUser, Text: "fix the bug"}
	if got := UserEntries(plain); len(got) != 1 || !reflect.DeepEqual(got[0], plain) {
		t.Errorf("plain prompt = %+v", got)
	}
	one := Entry{Kind: EntryUser, Text: "<argus-message id=\"m-1\" kind=\"text\" from=\"a\">\nhi\n</argus-message>\n\nfooter"}
	got := UserEntries(one)
	if len(got) != 1 || got[0].ID != "" || got[0].Text != "<argus-message id=\"m-1\" kind=\"text\" from=\"a\">\nhi\n</argus-message>" {
		t.Errorf("one message without an ID = %+v", got)
	}
}

func TestUserEntriesNodeQualifiedID(t *testing.T) {
	text := `<argus-message id="home:m-1a2b3c4d" kind="reply" from="worker-api" session="home:argus:%5">
Done.
</argus-message>`
	msgs := ParseArgusMessages(text)
	if len(msgs) != 1 || msgs[0].ID != "home:m-1a2b3c4d" {
		t.Fatalf("messages = %+v", msgs)
	}
	entries := UserEntries(Entry{ID: "e1", Kind: EntryUser, Text: text})
	var found bool
	for _, e := range entries {
		if e.ID == "e1.home:m-1a2b3c4d" {
			found = true
		}
	}
	if !found {
		t.Fatalf("entries = %+v", entries)
	}
}
