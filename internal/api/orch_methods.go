package api

import (
	"errors"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// CallerInfo identifies the process that issued an orch.* call.
type CallerInfo struct {
	PID int               `json:"pid"`
	Env map[string]string `json:"env,omitempty"` // only the adapters' caller identity variables
}

var ErrNoCaller = errors.New("not running inside an argus agent session")

// Method names for orch.* calls. IDs in their params and results are composite.
const (
	MethodOrchSessionCurrent     = "orch.session.current"     // request: CallerParams; result: session.Session
	MethodOrchSessionList        = "orch.session.list"        // request: CallerParams; result: OrchListResult
	MethodOrchSessionGet         = "orch.session.get"         // request: OrchTargetParams; result: OrchGetResult
	MethodOrchSessionSpawn       = "orch.session.spawn"       // request: OrchSpawnParams; result: OrchSpawnResult
	MethodOrchMessageSend        = "orch.message.send"        // request: OrchSendParams; result: OrchSendResult
	MethodOrchMessageReply       = "orch.message.reply"       // request: OrchReplyParams; result: OrchSendResult
	MethodOrchInteractionRespond = "orch.interaction.respond" // request: OrchRespondParams; result: nil
	MethodOrchSessionOutput      = "orch.session.output"      // request: OrchTargetParams; result: OrchOutputResult
	MethodOrchSessionTranscript  = "orch.session.transcript"  // request: OrchTranscriptParams; result: OrchTranscriptResult
	MethodOrchSessionKill        = "orch.session.kill"        // request: OrchTargetParams; result: OrchKillResult
	MethodOrchSessionRename      = "orch.session.rename"      // request: OrchRenameParams; result: SessionRenameResult
)

type CallerParams struct {
	Caller CallerInfo `json:"caller"`
}

type OrchListResult struct {
	Sessions []session.Session `json:"sessions"`
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

// OrchKillResult lists the composite IDs of the killed sessions: the target's
// live descendants, deepest first, then the target.
type OrchKillResult struct {
	Killed []string `json:"killed"`
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
