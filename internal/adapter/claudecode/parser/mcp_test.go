package parser_test

import (
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

func TestMCPToolResults(t *testing.T) {
	// MCP tool results carry toolUseResult as a JSON array (not object), which
	// previously made ParseEntry fail silently. Fixture: 3 MCP calls + results.
	path := filepath.Join("testdata", "mcp-tools.jsonl")
	chunks, err := parser.ReadSession(path)
	if err != nil {
		t.Fatalf("ReadSession(%q) error: %v", path, err)
	}

	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2 (user + AI)", len(chunks))
	}

	ai := chunks[1]
	if ai.Type != parser.AIChunk {
		t.Fatalf("chunks[1].Type = %d, want AIChunk", ai.Type)
	}

	// Collect tool call items.
	type toolInfo struct {
		name   string
		result string
	}
	var tools []toolInfo
	for _, item := range ai.Items {
		if item.Type == parser.ItemToolCall {
			tools = append(tools, toolInfo{name: item.ToolName, result: item.ToolResult})
		}
	}

	wantTools := []string{
		"mcp__context7__resolve-library-id",
		"ListMcpResourcesTool",
		"mcp__context7__query-docs",
	}
	if len(tools) != len(wantTools) {
		t.Fatalf("tool call count = %d, want %d", len(tools), len(wantTools))
	}

	for i, want := range wantTools {
		if tools[i].name != want {
			t.Errorf("tool[%d].name = %q, want %q", i, tools[i].name, want)
		}
		if tools[i].result == "" {
			t.Errorf("tool %q has empty ToolResult", want)
		}
	}
}
