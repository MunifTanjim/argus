package api

import (
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

const (
	PartyOwner = "owner"
	PartyArgus = "argus"
)

// Party identifies a message sender or recipient: a node and a party key.
type Party struct {
	Node string `json:"node"`
	Key  string `json:"key"`
}

type MessageKind string

const (
	MessageText           MessageKind = "text"
	MessageTask           MessageKind = "task"
	MessageReply          MessageKind = "reply"
	MessagePromptNotice   MessageKind = "prompt_notice"
	MessageTurnNotice     MessageKind = "turn_notice"
	MessageAnsweredNotice MessageKind = "answered_notice"
)

type MessageState string

const (
	MessageQueued    MessageState = "queued"
	MessageDelivered MessageState = "delivered"
	MessageReplied   MessageState = "replied"
	MessageWithdrawn MessageState = "withdrawn"
	MessageFailed    MessageState = "failed"
)

type Message struct {
	ID          string               `json:"id"`
	Kind        MessageKind          `json:"kind"`
	From        Party                `json:"from"`
	To          Party                `json:"to"`
	Body        string               `json:"body"`
	ReplyTo     string               `json:"reply_to,omitempty"`
	Subject     string               `json:"subject,omitempty"`     // notices: the child's party key
	Interaction *session.Interaction `json:"interaction,omitempty"` // prompt notices
	State       MessageState         `json:"state"`
	AnsweredBy  string               `json:"answered_by,omitempty"` // prompt notices: party key that answered
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// Link records that a child session was spawned by a parent.
type Link struct {
	Child   Party  `json:"child"`
	Parent  Party  `json:"parent"`
	TaskID  string `json:"task_id,omitempty"`
	EndedBy string `json:"ended_by,omitempty"` // "owner" | "parent" | "self"
}

// Method names for the owner's message view. Message IDs are "m-" plus 8 hex characters.
const (
	MethodMessagesList    = "messages.list"    // request: no params; result: MessagesListResult
	MethodMessagesSend    = "messages.send"    // request: OwnerSendParams; result: AgentSendResult
	MethodMessagesChanged = "messages.changed" // notification: empty params; clients refetch messages.list
)

type OwnerSendParams struct {
	SessionID string `json:"session_id,omitempty"` // bare local id; required unless ReplyTo is set
	Text      string `json:"text"`
	ReplyTo   string `json:"reply_to,omitempty"`
}

type MessagesListResult struct {
	Messages []Message `json:"messages"`
	Links    []Link    `json:"links"`
}
