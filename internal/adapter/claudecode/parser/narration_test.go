package parser_test

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

// protoField encodes one length-delimited protobuf field.
func protoField(num byte, val []byte) []byte {
	out := []byte{num<<3 | 2, byte(len(val))}
	return append(out, val...)
}

// fakeSignature builds a signature with kind at path 2 → 1 → 8, next to
// other fields like the ones in real signatures.
func fakeSignature(kind string) string {
	meta := append([]byte{0x08, 0x01}, protoField(8, []byte(kind))...) // varint field 1, then kind
	envelope := append(protoField(3, []byte("xx")), protoField(1, meta)...)
	sig := append(protoField(1, []byte("ciphertext")), protoField(2, envelope)...)
	return base64.StdEncoding.EncodeToString(sig)
}

func TestIsNarration(t *testing.T) {
	tests := []struct {
		name string
		sig  string
		want bool
	}{
		{"narration kind", fakeSignature("narration"), true},
		{"thinking kind", fakeSignature("thinking"), false},
		{"empty", "", false},
		{"not base64", "!!!", false},
		{"not protobuf", base64.StdEncoding.EncodeToString([]byte{0xff, 0xff, 0xff}), false},
		{"truncated", fakeSignature("narration")[:12], false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parser.IsNarration(tt.sig); got != tt.want {
				t.Errorf("IsNarration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassify_AssistantBlocks_NarrationAsText(t *testing.T) {
	blocks := []map[string]string{
		{"type": "thinking", "thinking": "Reasoning.", "signature": fakeSignature("thinking")},
		{"type": "thinking", "thinking": "I'm restarting the server.", "signature": fakeSignature("narration")},
		{"type": "thinking", "thinking": "", "signature": fakeSignature("narration")},
	}
	content, _ := json.Marshal(blocks)

	e := makeEntry("assistant", "a1", "2025-01-15T10:00:00Z", content,
		withModel("claude-opus-4-6"), withStopReason("tool_use"))

	msg, ok := parser.Classify(e)
	if !ok {
		t.Fatal("expected Classify to succeed")
	}
	ai := msg.(parser.AIMsg)

	want := []parser.ContentBlock{
		{Type: "thinking", Text: "Reasoning."},
		{Type: "text", Text: "I'm restarting the server."},
		{Type: "thinking", Text: ""},
	}
	if !reflect.DeepEqual(ai.Blocks, want) {
		t.Errorf("Blocks = %+v, want %+v", ai.Blocks, want)
	}
	if ai.Text != "I'm restarting the server." {
		t.Errorf("Text = %q, want the narration text", ai.Text)
	}
	if ai.ThinkingCount != 2 {
		t.Errorf("ThinkingCount = %d, want 2", ai.ThinkingCount)
	}
}
