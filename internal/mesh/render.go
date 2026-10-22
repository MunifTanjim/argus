package mesh

import (
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
)

// Render formats a message as the text typed into the recipient's prompt.
func Render(m api.Message, senderLabel, senderSessionID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[argus message %s from %s", m.ID, senderLabel)
	if senderSessionID != "" {
		fmt.Fprintf(&b, " (%s)", senderSessionID)
	}
	b.WriteString("]\n")
	b.WriteString(m.Body)
	b.WriteString("\n")
	switch m.Kind {
	case api.MessagePromptNotice, api.MessageTurnNotice, api.MessageAnsweredNotice:
	case api.MessageTask:
		fmt.Fprintf(&b, "When the task is done, report the result with the argus `reply` tool (message_id %s), or: argus session reply %s \"<result>\"", m.ID, m.ID)
	default:
		fmt.Fprintf(&b, "Reply with the argus `reply` tool (message_id %s), or: argus session reply %s \"<answer>\"", m.ID, m.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}
