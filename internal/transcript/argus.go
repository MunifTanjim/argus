package transcript

import (
	"regexp"
	"strings"
)

// ArgusMessage is one <argus-message> block of a user prompt, as
// internal/orch/render.go writes it.
type ArgusMessage struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`              // text, task, reply, interaction, turn_ended, interaction_ended
	From         string `json:"from,omitempty"`    // sender label: "owner", "argus", or a session label
	Session      string `json:"session,omitempty"` // sender's agent ID
	ReplyTo      string `json:"replyTo,omitempty"`
	Child        string `json:"child,omitempty"`        // notices: the child's label
	ChildSession string `json:"childSession,omitempty"` // notices: the child's agent ID
	Body         string `json:"body"`
}

const argusOpenTag = "<argus-message "

var (
	argusBlock         = regexp.MustCompile(`(?s)<argus-message ([^>]*)>\n(.*?)\n</argus-message>`)
	argusAttr          = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)
	argusAttrUnescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&amp;", "&")
)

// ParseArgusMessages reads the blocks of a prompt argus delivered. It returns
// nil unless text starts with a block; the footer after the last block is the
// agent's and is left out.
func ParseArgusMessages(text string) []ArgusMessage {
	msgs, _ := parseArgusBlocks(text)
	return msgs
}

// parseArgusBlocks is ParseArgusMessages, with each message's raw block.
func parseArgusBlocks(text string) (msgs []ArgusMessage, raw []string) {
	if !strings.HasPrefix(text, argusOpenTag) {
		return nil, nil
	}
	for _, m := range argusBlock.FindAllStringSubmatch(text, -1) {
		var msg ArgusMessage
		for _, a := range argusAttr.FindAllStringSubmatch(m[1], -1) {
			v := argusAttrUnescaper.Replace(a[2])
			switch a[1] {
			case "id":
				msg.ID = v
			case "kind":
				msg.Kind = v
			case "from":
				msg.From = v
			case "session":
				msg.Session = v
			case "reply-to":
				msg.ReplyTo = v
			case "child":
				msg.Child = v
			case "child-session":
				msg.ChildSession = v
			}
		}
		msg.Body = strings.ReplaceAll(m[2], "&lt;/argus-message>", "</argus-message>")
		msgs = append(msgs, msg)
		raw = append(raw, m[0])
	}
	return msgs, raw
}

// UserEntries parses a user entry's Text for argus messages and splits it into
// one entry per message, each with its own raw block as Text and that message
// as ArgusMessages; the footer after the blocks is the agent's and is dropped.
// Any other entry, or a user entry with no messages, comes back as is. A
// split entry's ID is e's ID and the message ID; an adapter that numbers
// entries by position leaves e's ID empty and numbers these after.
func UserEntries(e Entry) []Entry {
	if e.Kind != EntryUser {
		return []Entry{e}
	}
	msgs, raw := parseArgusBlocks(e.Text)
	if len(msgs) == 0 {
		return []Entry{e}
	}
	out := make([]Entry, len(msgs))
	for i, msg := range msgs {
		s := e
		s.Text, s.ArgusMessages = raw[i], []ArgusMessage{msg}
		if e.ID != "" {
			s.ID = e.ID + "." + msg.ID
		}
		out[i] = s
	}
	return out
}

// ArgusTaskLine is the first non-empty line of the first message of text,
// trimmed; ok is false when text holds no argus messages.
func ArgusTaskLine(text string) (line string, ok bool) {
	msgs := ParseArgusMessages(text)
	if len(msgs) == 0 {
		return "", false
	}
	for _, l := range strings.Split(msgs[0].Body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l, true
		}
	}
	return "", true
}
