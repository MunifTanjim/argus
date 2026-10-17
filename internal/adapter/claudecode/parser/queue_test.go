package parser_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

func queueOp(t *testing.T, line string) (parser.QueueOp, bool) {
	t.Helper()
	var e parser.Entry
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatal(err)
	}
	return parser.QueueOpFrom(e)
}

func TestQueueOpFrom(t *testing.T) {
	op, ok := queueOp(t, `{"type":"queue-operation","operation":"remove","timestamp":"2026-10-16T14:45:23.876Z","sessionId":"s","content":"P1 say ONE","reason":"absorbed_mid_turn"}`)
	if !ok || op != (parser.QueueOp{Op: "remove", Content: "P1 say ONE"}) {
		t.Fatalf("got %+v, %v", op, ok)
	}
	if op, ok := queueOp(t, `{"content":"P2","operation":"enqueue","type":"queue-operation"}`); !ok || op.Content != "P2" {
		t.Fatalf("keys in another order: %+v, %v", op, ok)
	}
	if op, ok := queueOp(t, `{"type":"queue-operation","operation":"dequeue","timestamp":"t","sessionId":"s"}`); !ok || op.Content != "" {
		t.Fatalf("bare dequeue: %+v, %v", op, ok)
	}
	if _, ok := queueOp(t, `{"type":"user","uuid":"u1","message":{"content":"queue-operation"}}`); ok {
		t.Fatal("a user line is not a queue record")
	}
}

func TestReadSessionIncrementalWithQueueSplitsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	content := `{"type":"user","uuid":"u1","timestamp":"2026-10-16T14:45:00Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-16T14:45:14.858Z","sessionId":"s","content":"P1 say ONE"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, _, ops, _, err := parser.ReadSessionIncrementalWithQueue(path, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || len(ops) != 1 || ops[0].Op != "enqueue" || ops[0].Content != "P1 say ONE" {
		t.Fatalf("msgs %d, ops %+v", len(msgs), ops)
	}
}
