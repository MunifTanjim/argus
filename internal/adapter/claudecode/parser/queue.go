package parser

import "encoding/json"

// QueueOp is one queue-operation record: Claude's log of its prompt queue
// changing. Op is enqueue, dequeue, remove, popAll or popOne.
type QueueOp struct {
	Op      string
	Content string
}

// QueueOpFrom reads a queue-operation entry. Such entries carry no uuid, so
// ParseEntry drops them.
func QueueOpFrom(e Entry) (QueueOp, bool) {
	if e.Type != "queue-operation" {
		return QueueOp{}, false
	}
	var content string
	_ = json.Unmarshal(e.Content, &content) // content blocks or none read as ""
	return QueueOp{Op: e.Operation, Content: content}, true
}
