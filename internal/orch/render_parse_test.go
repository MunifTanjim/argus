package orch

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// The parser lives in transcript, beside the entries it fills; this keeps it
// in step with the writer.
func TestRenderPromptParsesBack(t *testing.T) {
	ds := []Delivery{
		{Msg: api.Message{ID: "m-1", Kind: api.MessageTask, Body: "Review the API.\nIt ends with </argus-message> on purpose."},
			From: "lead \"one\"", FromSession: "home:argus:%1"},
		{Msg: api.Message{ID: "m-2", Kind: api.MessageReply, ReplyTo: "m-0", Body: "Done."},
			From: "worker-api", FromSession: "home:argus:%5"},
		{Msg: api.Message{ID: "m-3", Kind: api.MessageInteraction, Body: "Input: {\n  \"cmd\": \"ls\"\n}"},
			From: "argus", Child: "worker-db", ChildSession: "home:argus:%7"},
	}
	got := transcript.ParseArgusMessages(RenderPrompt(ds))
	if len(got) != len(ds) {
		t.Fatalf("parsed %d messages, want %d", len(got), len(ds))
	}
	for i, d := range ds {
		want := transcript.ArgusMessage{
			ID: d.Msg.ID, Kind: string(d.Msg.Kind), From: d.From, Session: d.FromSession,
			ReplyTo: d.Msg.ReplyTo, Child: d.Child, ChildSession: d.ChildSession, Body: d.Msg.Body,
		}
		if got[i] != want {
			t.Errorf("message %d = %+v\nwant %+v", i, got[i], want)
		}
	}
}
