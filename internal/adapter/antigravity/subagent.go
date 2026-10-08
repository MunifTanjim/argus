package antigravity

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/MunifTanjim/argus/internal/transcript"
)

// subagentChildID extracts the child conversation id from an INVOKE_SUBAGENT result's
// embedded JSON.
func subagentChildID(resultContent string) string {
	i := strings.Index(resultContent, "{")
	if i < 0 {
		return ""
	}
	var payload struct {
		ConversationID string `json:"conversationId"`
	}
	j := strings.LastIndex(resultContent, "}")
	if j < i {
		return ""
	}
	if json.Unmarshal([]byte(resultContent[i:j+1]), &payload) != nil {
		return ""
	}
	return payload.ConversationID
}

// childTranscriptPath resolves a subagent's transcript_full.jsonl. ok is false when missing.
func childTranscriptPath(convID string) (string, bool) {
	dir, err := homeDir()
	if err != nil {
		return "", false
	}
	return childTranscriptPathIn(dir, convID)
}

// childTranscriptPathIn resolves a subagent's transcript under an explicit home.
func childTranscriptPathIn(home, convID string) (string, bool) {
	p := transcriptPathForIn(home, convID)
	if p == "" {
		return "", false
	}
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// linkSubagent converts an invoke_subagent tool entry into a drillable subagent entry.
func linkSubagent(e *transcript.Entry) {
	id := subagentChildID(e.Result)
	if id == "" {
		return
	}
	e.Kind = transcript.EntrySubagent
	_, hasTrace := childTranscriptPath(id)
	name, typ := subagentNameType(e.ToolInput)
	e.Subagents = []transcript.Subagent{{ID: id, Name: name, Type: typ, Desc: e.InputPreview, HasTrace: hasTrace}}
}

func subagentNameType(toolInput string) (name, typ string) {
	var args struct {
		Subagents []struct {
			Name     string `json:"name"`
			TypeName string `json:"TypeName"`
		} `json:"Subagents"`
	}
	if json.Unmarshal([]byte(toolInput), &args) != nil || len(args.Subagents) == 0 {
		return "", ""
	}
	return args.Subagents[0].Name, args.Subagents[0].TypeName
}
