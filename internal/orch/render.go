package orch

import (
	"fmt"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
)

// Delivery is one message and the labels its block needs.
type Delivery struct {
	Msg          api.Message
	From         string // sender label: "owner", "argus", or a session label
	FromSession  string // sender agent ID; "" for owner and argus
	Child        string // notices: the child's label
	ChildSession string // notices: the child's agent ID
}

const (
	closingTag = "</argus-message>"

	taskFooter = "Do the task yourself. Spawn no argus sessions unless the task says to.\n" +
		"When a task is done, report the result with the argus `reply_to_message` tool (message_id is the task's id), or: argus session reply <id> \"<result>\""
	replyFooter   = "Reply to a message with the argus `reply_to_message` tool (message_id is the message's id), or: argus session reply <id> \"<answer>\""
	respondFooter = "Answer a question with the argus `respond_to_interaction` tool (message_id is the message's id), or:\n" +
		"  argus session respond <id> deny [--reason \"<text>\"]\n" +
		"  argus session respond <id> --answer \"<question>=<option label>\""
)

var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// RenderPrompt formats ds as the text typed into the recipient's prompt: one
// <argus-message> block per delivery, then the hint lines the blocks need.
func RenderPrompt(ds []Delivery) string {
	blocks := make([]string, 0, len(ds))
	var task, reply, respond bool
	for _, d := range ds {
		blocks = append(blocks, block(d))
		switch d.Msg.Kind {
		case api.MessageTask:
			task = true
		case api.MessageText, api.MessageReply:
			reply = true
		case api.MessageInteraction:
			respond = respond || d.Msg.Interaction.ParentCanAnswer()
		}
	}
	var footer []string
	if task {
		footer = append(footer, taskFooter)
	}
	if reply {
		footer = append(footer, replyFooter)
	}
	if respond {
		footer = append(footer, respondFooter)
	}
	out := strings.Join(blocks, "\n\n")
	if len(footer) > 0 {
		out += "\n\n" + strings.Join(footer, "\n")
	}
	return out
}

func block(d Delivery) string {
	var b strings.Builder
	b.WriteString("<argus-message")
	attr(&b, "id", d.Msg.ID)
	attr(&b, "kind", string(d.Msg.Kind))
	attr(&b, "from", d.From)
	attr(&b, "session", d.FromSession)
	attr(&b, "reply-to", d.Msg.ReplyTo)
	attr(&b, "child", d.Child)
	attr(&b, "child-session", d.ChildSession)
	b.WriteString(">\n")
	b.WriteString(strings.ReplaceAll(d.Msg.Body, closingTag, "&lt;/argus-message>"))
	b.WriteString("\n" + closingTag)
	return b.String()
}

func attr(b *strings.Builder, name, value string) {
	if value != "" {
		fmt.Fprintf(b, ` %s="%s"`, name, attrEscaper.Replace(value))
	}
}
