package parser

import "encoding/json"

// Entry represents a raw JSONL line from a Claude Code session file.
// Fields map directly to the on-disk format at ~/.claude/projects/{project}/{session}.jsonl.
type Entry struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	Timestamp   string `json:"timestamp"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Model      string          `json:"model"`
		StopReason *string         `json:"stop_reason"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`

	// Session-level metadata. Present on most entry types.
	Cwd            string `json:"cwd"`
	GitBranch      string `json:"gitBranch"`
	PermissionMode string `json:"permissionMode"` // "default", "acceptEdits", "bypassPermissions", "plan"

	// ToolUseResult is a tool's structured output: a JSON object for regular
	// tools, but a JSON array for MCP tools — RawMessage tolerates both. Use
	// ToolUseResultMap() for object access.
	ToolUseResult   json.RawMessage `json:"toolUseResult"`
	SourceToolUseID string          `json:"sourceToolUseID"`

	// Summary entries use leafUuid (not uuid) and carry the compression title
	// in Summary (not message.content).
	LeafUUID string `json:"leafUuid"`
	Summary  string `json:"summary"`

	// Attachment payload (Claude Code 2.1+ UI side-events). Only nested_memory
	// (the "Loaded X" pill) is surfaced; other subtypes are dropped by Classify.
	// Body omitted by design — we show the path, not file contents.
	Attachment struct {
		Type        string `json:"type"`
		DisplayPath string `json:"displayPath"`
	} `json:"attachment"`
}

// ToolUseResultMap attempts to parse ToolUseResult as a JSON object.
// Returns nil if ToolUseResult is absent, empty, or a non-object type (e.g.
// the JSON array that MCP tools produce).
func (e Entry) ToolUseResultMap() map[string]json.RawMessage {
	if len(e.ToolUseResult) == 0 || e.ToolUseResult[0] != '{' {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(e.ToolUseResult, &m) != nil {
		return nil
	}
	return m
}

// ParseEntry parses a single JSONL line into an Entry.
// Returns false if the JSON is invalid or the entry has no UUID.
func ParseEntry(line []byte) (Entry, bool) {
	var e Entry
	if err := json.Unmarshal(line, &e); err != nil {
		return Entry{}, false
	}
	// Summary entries use leafUuid instead of uuid.
	if e.UUID == "" && e.LeafUUID == "" {
		return Entry{}, false
	}
	return e, true
}
