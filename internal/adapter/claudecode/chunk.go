package claudecode

import (
	"github.com/MunifTanjim/argus/internal/transcript"
)

// The entry model is argus's stable, display-ready view of a transcript, shipped
// over RPC to the clients.

type (
	Usage          = transcript.Usage
	EntryKind      = transcript.EntryKind
	Entry          = transcript.Entry
	Subagent       = transcript.Subagent
	TranscriptView = transcript.TranscriptView
	ToolDetail     = transcript.ToolDetail
)

const (
	EntryUser     = transcript.EntryUser
	EntryThinking = transcript.EntryThinking
	EntryText     = transcript.EntryText
	EntryTool     = transcript.EntryTool
	EntrySubagent = transcript.EntrySubagent
	EntrySkill    = transcript.EntrySkill
	EntryTurnEnd  = transcript.EntryTurnEnd
	EntrySystem   = transcript.EntrySystem
	EntryCompact  = transcript.EntryCompact
	EntryShell    = transcript.EntryShell
)
