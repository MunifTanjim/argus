package claudecode

import (
	"github.com/MunifTanjim/argus/internal/transcript"
)

// The chunk model is argus's stable, display-ready view of a transcript, shipped
// over RPC and rendered by the TUI. transcript.go maps the vendored parser's
// output into these types, so this boundary stays fixed if the parser changes.

type (
	Usage          = transcript.Usage
	ChunkKind      = transcript.ChunkKind
	ItemKind       = transcript.ItemKind
	Item           = transcript.Item
	Subagent       = transcript.Subagent
	Chunk          = transcript.Chunk
	TranscriptView = transcript.TranscriptView
	ToolDetail     = transcript.ToolDetail
)

const (
	ChunkUser    = transcript.ChunkUser
	ChunkAI      = transcript.ChunkAI
	ChunkSystem  = transcript.ChunkSystem
	ChunkCompact = transcript.ChunkCompact
	ChunkShell   = transcript.ChunkShell

	ItemThinking = transcript.ItemThinking
	ItemText     = transcript.ItemText
	ItemTool     = transcript.ItemTool
	ItemSubagent = transcript.ItemSubagent
	ItemSkill    = transcript.ItemSkill
	ItemPrompt   = transcript.ItemPrompt
)
