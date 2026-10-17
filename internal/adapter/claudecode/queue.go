package claudecode

import (
	"slices"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// promptQueue mirrors Claude's prompt queue from the transcript's
// queue-operation records. Machine items share the queue: they are kept,
// hidden, so a bare dequeue can take one instead of a prompt.
type promptQueue struct {
	items []queued
}

type queued struct {
	transcript.QueuedPrompt
	hidden bool
}

// machineTags open the queued text of items Claude injects itself.
var machineTags = []string{
	"<task-notification",
	"<teammate-message",
	"<channel",
	"<cross-session-message",
	"<agent-message",
	"<wake",
}

// machineHeaders are lines Claude may put before a machine envelope.
var machineHeaders = []string{
	"A message arrived from ",
	"Another Claude session sent a message",
	"A peer session sent a message",
}

func isMachineText(text string) bool {
	for _, h := range machineHeaders {
		if strings.HasPrefix(text, h) {
			if _, rest, ok := strings.Cut(text, "\n"); ok {
				text = rest
			}
			break
		}
	}
	for _, tag := range machineTags {
		if rest, ok := strings.CutPrefix(text, tag); ok &&
			(rest == "" || strings.ContainsRune(" >\t\n", rune(rest[0]))) {
			return true
		}
	}
	return false
}

func (q *promptQueue) apply(op parser.QueueOp) {
	switch op.Op {
	case "enqueue":
		q.items = append(q.items, queued{
			QueuedPrompt: transcript.QueuedPrompt{Text: op.Content},
			hidden:       op.Content == "" || isMachineText(op.Content),
		})
	case "remove", "popOne":
		if i := slices.IndexFunc(q.items, func(it queued) bool { return it.Text == op.Content }); i >= 0 {
			q.items = slices.Delete(q.items, i, i+1)
		}
	case "popAll":
		q.items = slices.DeleteFunc(q.items, func(it queued) bool {
			return !it.hidden && strings.Contains(op.Content, it.Text)
		})
	case "dequeue":
		// Claude runs a prompt typed in its input before a machine item.
		if len(q.items) > 0 {
			i := max(0, slices.IndexFunc(q.items, func(it queued) bool { return !it.hidden }))
			q.items = slices.Delete(q.items, i, i+1)
		}
	}
}

// prompts is the queued prompts, oldest first, never nil.
func (q *promptQueue) prompts() []transcript.QueuedPrompt {
	out := []transcript.QueuedPrompt{}
	for _, it := range q.items {
		if !it.hidden {
			out = append(out, it.QueuedPrompt)
		}
	}
	return out
}

func (q *promptQueue) clear() { q.items = nil }
