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
