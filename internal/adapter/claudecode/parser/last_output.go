package parser

// LastOutputType discriminates last output categories.
type LastOutputType int

const (
	LastOutputText LastOutputType = iota
	LastOutputToolResult
	LastOutputToolCalls // fallback: only tool names, no results yet
)

// LastOutput represents the final visible output from an AI turn.
// Used by the TUI to show "the answer" in collapsed message view.
type LastOutput struct {
	Type       LastOutputType
	Text       string            // LastOutputText: the output text
	ToolName   string            // LastOutputToolResult: which tool
	ToolResult string            // LastOutputToolResult: result content
	IsError    bool              // LastOutputToolResult: was it an error
	ToolCalls  []ToolCallSummary // LastOutputToolCalls: tool names when no output/results
}

// ToolCallSummary is a tool name + one-line summary for collapsed preview.
type ToolCallSummary struct {
	Name    string
	Summary string
}

// FindLastOutput finds the last meaningful output, in priority order:
// last output text, then last tool result, then tool-call names as fallback.
func FindLastOutput(items []DisplayItem) *LastOutput {
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.Type == ItemOutput && item.Text != "" {
			return &LastOutput{
				Type: LastOutputText,
				Text: item.Text,
			}
		}
	}

	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if (item.Type == ItemToolCall || item.Type == ItemSubagent) && item.ToolResult != "" {
			return &LastOutput{
				Type:       LastOutputToolResult,
				ToolName:   item.ToolName,
				ToolResult: item.ToolResult,
				IsError:    item.ToolError,
			}
		}
	}

	// Fallback: turns where the assistant only made tool calls, no output/results yet.
	const maxToolCalls = 5
	var calls []ToolCallSummary
	for _, item := range items {
		if item.Type == ItemToolCall || item.Type == ItemSubagent {
			calls = append(calls, ToolCallSummary{
				Name:    item.ToolName,
				Summary: item.ToolSummary,
			})
			if len(calls) >= maxToolCalls {
				break
			}
		}
	}
	if len(calls) > 0 {
		return &LastOutput{
			Type:      LastOutputToolCalls,
			ToolCalls: calls,
		}
	}

	return nil
}
