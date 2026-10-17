package claudecode

import (
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

const notif = "<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n</task-notification>"

func texts(q []transcript.QueuedPrompt) []string {
	out := []string{}
	for _, p := range q {
		out = append(out, p.Text)
	}
	return out
}

func TestPromptQueue(t *testing.T) {
	enq := func(c string) parser.QueueOp { return parser.QueueOp{Op: "enqueue", Content: c} }
	for _, tc := range []struct {
		name string
		ops  []parser.QueueOp
		want []string
	}{
		{"enqueue", []parser.QueueOp{enq("P1"), enq("P2")}, []string{"P1", "P2"}},
		{"absorbed mid-turn", []parser.QueueOp{enq("P1"), enq("P2"), {Op: "remove", Content: "P1"}}, []string{"P2"}},
		{"duplicate text removes one", []parser.QueueOp{enq("continue"), enq("continue"), {Op: "remove", Content: "continue"}}, []string{"continue"}},
		{"popAll", []parser.QueueOp{enq("P5"), {Op: "popAll", Content: "P5"}}, []string{}},
		{"popAll then resubmit", []parser.QueueOp{enq("P5"), {Op: "popAll", Content: "P5"}, enq("P5")}, []string{"P5"}},
		{"popOne", []parser.QueueOp{enq("P1"), enq("P2"), {Op: "popOne", Content: "P2"}}, []string{"P1"}},
		{"dequeue takes the oldest", []parser.QueueOp{enq("P3"), enq("P4"), {Op: "dequeue"}}, []string{"P4"}},
		{"notifications are hidden", []parser.QueueOp{enq(notif), enq("P1")}, []string{"P1"}},
		{"a notification's remove keeps prompts", []parser.QueueOp{enq(notif), enq("P1"), {Op: "remove", Content: notif}}, []string{"P1"}},
		{"dequeue prefers a prompt over a notification", []parser.QueueOp{enq(notif), enq("P1"), {Op: "dequeue"}, {Op: "dequeue"}}, []string{}},
		{"dequeue of a lone notification", []parser.QueueOp{enq(notif), {Op: "dequeue"}, enq("P1")}, []string{"P1"}},
		{"a content-less enqueue is hidden", []parser.QueueOp{{Op: "enqueue"}, enq("P1")}, []string{"P1"}},
		{"a bare dequeue takes the content-less enqueue last", []parser.QueueOp{{Op: "enqueue"}, enq("P1"), {Op: "dequeue"}}, []string{}},
		{"a bare dequeue after a content-less enqueue keeps the later prompt", []parser.QueueOp{{Op: "enqueue"}, {Op: "dequeue"}, enq("P1")}, []string{"P1"}},
		{"teammate message is hidden", []parser.QueueOp{enq(`<teammate-message teammate_id="a" summary="s">` + "\nhi\n</teammate-message>"), enq("P1")}, []string{"P1"}},
		{"channel message is hidden", []parser.QueueOp{enq("<channel source=\"slack\">\nhi\n</channel>"), enq("P1")}, []string{"P1"}},
		{"cross-session message is hidden", []parser.QueueOp{enq("<cross-session-message from=\"x\">\nhi\n</cross-session-message>"), enq("P1")}, []string{"P1"}},
		{"agent message is hidden", []parser.QueueOp{enq("<agent-message from=\"x\">\nhi\n</agent-message>"), enq("P1")}, []string{"P1"}},
		{"wake envelope is hidden", []parser.QueueOp{enq("<wake reason=\"external-event\">\n</wake>"), enq("P1")}, []string{"P1"}},
		{"a headed peer message is hidden", []parser.QueueOp{enq("Another Claude session sent a message while you were working:\n<cross-session-message from=\"x\">\nhi\n</cross-session-message>"), enq("P1")}, []string{"P1"}},
		{"a prompt that only resembles a tag is listed", []parser.QueueOp{enq("<channels> are fun")}, []string{"<channels> are fun"}},
	} {
		var q promptQueue
		for _, op := range tc.ops {
			q.apply(op)
		}
		if got := texts(q.prompts()); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
