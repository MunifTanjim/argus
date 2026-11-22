package api

import (
	"encoding/json"
	"errors"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// CallerInfo identifies the process that issued an orch.* call.
type CallerInfo struct {
	PID int               `json:"pid"`
	Env map[string]string `json:"env,omitempty"` // only the adapters' caller identity variables
	// Remote names a caller on another node; only that node's peer channel may send it.
	Remote *RemoteCaller `json:"remote,omitempty"`
}

// RemoteCaller is an agent session on another node, as that node states it.
type RemoteCaller struct {
	Actor Actor  `json:"actor"`
	Label string `json:"label"`
}

var ErrNoCaller = errors.New("not running inside an argus agent session")

// Method names for orch.* calls. Session IDs in their params and results are agent IDs.
const (
	MethodOrchSessionCurrent     = "orch.session.current"     // request: CallerParams; result: session.Session
	MethodOrchSessionList        = "orch.session.list"        // request: OrchListParams; result: OrchListResult
	MethodOrchSessionGet         = "orch.session.get"         // request: OrchTargetParams; result: OrchGetResult
	MethodOrchSessionSpawn       = "orch.session.spawn"       // request: OrchSpawnParams; result: OrchSpawnResult
	MethodOrchMessageSend        = "orch.message.send"        // request: OrchSendParams; result: OrchSendResult
	MethodOrchMessageReply       = "orch.message.reply"       // request: OrchReplyParams; result: OrchSendResult
	MethodOrchInteractionRespond = "orch.interaction.respond" // request: OrchRespondParams; result: nil
	MethodOrchSessionOutput      = "orch.session.output"      // request: OrchTargetParams; result: OrchOutputResult
	MethodOrchSessionTranscript  = "orch.session.transcript"  // request: OrchTranscriptParams; result: OrchTranscriptResult
	MethodOrchSessionKill        = "orch.session.kill"        // request: OrchKillParams; result: OrchKillResult
	MethodOrchSessionRename      = "orch.session.rename"      // request: OrchRenameParams; result: SessionRenameResult
)

// Methods one node pushes to another over its peer channel; a node accepts
// them only from a peer node, and only about that node's own actors. Each is
// idempotent, so a push retried after a lost reply is harmless.
const (
	MethodOrchPeerDeliver   = "orch.peer.deliver"    // request: OrchPeerDeliverParams; result: nil
	MethodOrchPeerWithdraw  = "orch.peer.withdraw"   // request: OrchPeerWithdrawParams; result: nil
	MethodOrchPeerLinkEnded = "orch.peer.link_ended" // request: OrchPeerLinkEndedParams; result: nil
	// request: OrchPeerParentEndedParams; result: nil
	MethodOrchPeerParentEnded = "orch.peer.parent_ended"
	// request: OrchPeerStateParams; result: nil. Needs cross_node_orch 2.
	MethodOrchPeerState = "orch.peer.state"
	// request: OrchPeerHandoffParams; result: nil. An agent of the sending node
	// asks an agent of the receiver for work.
	MethodOrchPeerHandoff = "orch.peer.handoff"
	// request: OrchPeerHandoffAnswerParams; result: nil. The answer to a
	// hand-off the receiver sent.
	MethodOrchPeerHandoffAnswer = "orch.peer.handoff_answer"
)

// OrchPeerHandoffParams is a hand-off from an agent of the sending node.
// Ancestors are the agents up the chain as "node:agent", the asker last.
type OrchPeerHandoffParams struct {
	HandoffID string          `json:"handoff_id"`
	RootID    string          `json:"root_id"`
	Depth     int             `json:"depth"`
	Ancestors []string        `json:"ancestors"`
	FromAgent string          `json:"from_agent"`
	FromName  string          `json:"from_name"`
	ToAgent   string          `json:"to_agent"`
	Request   json.RawMessage `json:"request"`
}

// OrchPeerHandoffAnswerParams ends a hand-off: State is answered or
// no_answer; Text is the answer verbatim, or why there is none.
type OrchPeerHandoffAnswerParams struct {
	HandoffID string `json:"handoff_id"`
	State     string `json:"state"`
	Result    string `json:"result,omitempty"`
	Text      string `json:"text"`
}

// OrchPeerDeliverParams carries a message the sending node created, with its ID and timestamps.
type OrchPeerDeliverParams struct {
	Message Message `json:"message"`
}

// OrchPeerWithdrawParams withdraws an open message the sending node created.
// Ended is the interaction_ended message the receiver gives the recipient when it
// already delivered the message. AnsweredBy, when set, names the actor on the
// receiving node that answered the interaction: the receiver marks its copy
// answered by that actor and gives no interaction_ended message.
type OrchPeerWithdrawParams struct {
	MessageID  string `json:"message_id"`
	Ended      string `json:"ended,omitempty"`
	AnsweredBy *Actor `json:"answered_by,omitempty"`
}

// OrchPeerStateParams moves the sending node's copy of a message to State:
// delivered, replied, or failed. The receiver created the message.
type OrchPeerStateParams struct {
	MessageID string       `json:"message_id"`
	State     MessageState `json:"state"`
}

// OrchPeerParentEndedParams records that a parent on the sending node ended.
type OrchPeerParentEndedParams struct {
	ParentKey string `json:"parent_key"`
}

// OrchPeerLinkEndedParams records how a child on the sending node ended.
type OrchPeerLinkEndedParams struct {
	ChildKey string `json:"child_key"`
	EndedBy  string `json:"ended_by"`
}

type CallerParams struct {
	Caller CallerInfo `json:"caller"`
}

type OrchListParams struct {
	Caller CallerInfo `json:"caller"`
	// All asks for the sessions of every online node, not only this one.
	All bool `json:"all,omitempty"`
}

type OrchListResult struct {
	Sessions []session.Session `json:"sessions"`
	// Unreachable is the IDs of nodes whose sessions are missing from Sessions.
	Unreachable []string `json:"unreachable,omitempty"`
}

type OrchTargetParams struct {
	Caller    CallerInfo `json:"caller"`
	SessionID string     `json:"session_id"`
}

type OrchGetResult struct {
	Session session.Session `json:"session"`
	Queued  []Message       `json:"queued"`
}

type OrchSpawnParams struct {
	Caller CallerInfo `json:"caller"`
	Agent  string     `json:"agent"`
	Cwd    string     `json:"cwd"`
	Name   string     `json:"name,omitempty"`
	Model  string     `json:"model,omitempty"`
	Prompt string     `json:"prompt,omitempty"`
	// RequestID makes a retry return the first result instead of spawning again.
	RequestID string `json:"request_id,omitempty"`
	// Node is the ID or label of the node to spawn on; empty means this node.
	Node string `json:"node,omitempty"`
}

type OrchSpawnResult struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id,omitempty"`
}

type OrchSendParams struct {
	Caller    CallerInfo `json:"caller"`
	SessionID string     `json:"session_id"`
	Text      string     `json:"text"`
	// RequestID makes a retry return the first message instead of sending again.
	RequestID string `json:"request_id,omitempty"`
}

type OrchSendResult struct {
	MessageID string `json:"message_id"`
}

type OrchReplyParams struct {
	Caller    CallerInfo `json:"caller"`
	MessageID string     `json:"message_id"`
	Text      string     `json:"text"`
}

type OrchRespondParams struct {
	Caller    CallerInfo     `json:"caller"`
	MessageID string         `json:"message_id"`
	Behavior  string         `json:"behavior,omitempty"` // "allow" | "deny"
	Reason    string         `json:"reason,omitempty"`
	Answers   map[string]any `json:"answers,omitempty"` // question text → option label
}

// OrchRenameParams names any session; no caller is needed.
type OrchRenameParams struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

// OrchKillParams is a kill request. Ancestor is honored only from a peer node
// killing a child of a session it is killing: the child ends by "ancestor".
type OrchKillParams struct {
	OrchTargetParams
	Ancestor bool `json:"ancestor,omitempty"`
}

// OrchKillResult lists the agent IDs of the killed sessions: the target's
// live descendants, deepest first, then the target. NotKilled lists the
// descendants on other nodes that could not be reached or killed.
type OrchKillResult struct {
	Killed    []string `json:"killed"`
	NotKilled []string `json:"not_killed,omitempty"`
}

type OrchOutputResult struct {
	Text string `json:"text"`
}

type OrchTranscriptParams struct {
	Caller    CallerInfo `json:"caller"`
	SessionID string     `json:"session_id"`
	Last      int        `json:"last,omitempty"` // 0 = all entries
}

type OrchTranscriptResult struct {
	Entries []transcript.Entry `json:"entries"`
}
