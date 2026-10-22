package api

import (
	"time"

	"github.com/MunifTanjim/argus/internal/session"
)

const (
	ActorOwner = "owner"
	ActorArgus = "argus"
)

// Actor identifies a message sender or recipient: a node and an actor key.
type Actor struct {
	Node string `json:"node"`
	Key  string `json:"key"`
}

type MessageKind string

const (
	MessageText             MessageKind = "text"
	MessageTask             MessageKind = "task"
	MessageReply            MessageKind = "reply"
	MessageInteraction      MessageKind = "interaction"
	MessageTurnEnded        MessageKind = "turn_ended"
	MessageInteractionEnded MessageKind = "interaction_ended"
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
	ID      string      `json:"id"`
	Kind    MessageKind `json:"kind"`
	From    Actor       `json:"from"`
	To      Actor       `json:"to"`
	Body    string      `json:"body"`
	ReplyTo string      `json:"reply_to,omitempty"`
	// Child is the child session a notice is about.
	Child       *Actor               `json:"child,omitempty"`
	Interaction *session.Interaction `json:"interaction,omitempty"` // interaction messages
	State       MessageState         `json:"state"`
	// AnsweredBy is the actor that answered an interaction message.
	AnsweredBy *Actor    `json:"answered_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Link records that a child session was spawned by a parent.
type Link struct {
	Child   Actor  `json:"child"`
	Parent  Actor  `json:"parent"`
	TaskID  string `json:"task_id,omitempty"`
	EndedBy string `json:"ended_by,omitempty"` // "owner" | "parent" | "self" | "ancestor"
	// ChildSessionID is the child's composite session ID, as known when the
	// link was made.
	ChildSessionID string `json:"child_session_id,omitempty"`
}

// Method names for the owner's message view. Message IDs are the node ID, ":m-", and 16 hex characters.
const (
	MethodMessageList    = "message.list"    // request: no params; result: MessagesListResult
	MethodMessageSend    = "message.send"    // request: OwnerSendParams; result: OrchSendResult
	MethodMessageChanged = "message.changed" // notification: empty params; clients refetch message.list
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
