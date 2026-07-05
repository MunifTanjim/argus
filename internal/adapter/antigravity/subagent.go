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
	p := transcriptPathFor(convID)
	if p == "" {
		return "", false
	}
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// linkSubagent converts an invoke_subagent tool item into a drillable subagent item.
func linkSubagent(it *transcript.Item) {
	id := subagentChildID(it.Result)
	if id == "" {
		return
	}
	it.Kind = transcript.ItemSubagent
	_, hasTrace := childTranscriptPath(id)
	name, typ := subagentNameType(it.ToolInput)
	it.Subagents = []transcript.Subagent{{ID: id, Name: name, Type: typ, Desc: it.InputPreview, HasTrace: hasTrace}}
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
