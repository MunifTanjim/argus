package api

import (
	"errors"

	"github.com/MunifTanjim/argus/internal/session"
	"github.com/MunifTanjim/argus/internal/transcript"
)

// CallerInfo identifies the process that issued an agentctl call.
type CallerInfo struct {
	PID int               `json:"pid"`
	Env map[string]string `json:"env,omitempty"` // only the adapters' caller identity variables
}

var ErrNoCaller = errors.New("not running inside an argus agent session")

// Method names for agentctl calls. IDs in their params and results are composite.
const (
	MethodAgentWhoami     = "agentctl.whoami"     // request: CallerParams; result: session.Session
	MethodAgentList       = "agentctl.list"       // request: CallerParams; result: []session.Session
	MethodAgentShow       = "agentctl.show"       // request: AgentTargetParams; result: AgentShowResult
	MethodAgentSpawn      = "agentctl.spawn"      // request: AgentSpawnParams; result: AgentSpawnResult
	MethodAgentSend       = "agentctl.send"       // request: AgentSendParams; result: AgentSendResult
	MethodAgentReply      = "agentctl.reply"      // request: AgentReplyParams; result: AgentSendResult
	MethodAgentRespond    = "agentctl.respond"    // request: AgentRespondParams; result: nil
	MethodAgentOutput     = "agentctl.output"     // request: AgentTargetParams; result: AgentOutputResult
	MethodAgentTranscript = "agentctl.transcript" // request: AgentTranscriptParams; result: AgentTranscriptResult
	MethodAgentKill       = "agentctl.kill"       // request: AgentTargetParams; result: nil
)

type CallerParams struct {
	Caller CallerInfo `json:"caller"`
}

type AgentTargetParams struct {
	Caller    CallerInfo `json:"caller"`
	SessionID string     `json:"session_id"`
}

type AgentShowResult struct {
	Session session.Session `json:"session"`
	Queued  []Message       `json:"queued"`
}

type AgentSpawnParams struct {
	Caller CallerInfo `json:"caller"`
	Agent  string     `json:"agent"`
	Cwd    string     `json:"cwd"`
	Name   string     `json:"name,omitempty"`
	Model  string     `json:"model,omitempty"`
	Prompt string     `json:"prompt,omitempty"`
}

type AgentSpawnResult struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id,omitempty"`
}

type AgentSendParams struct {
	Caller    CallerInfo `json:"caller"`
	SessionID string     `json:"session_id"`
	Text      string     `json:"text"`
}

type AgentSendResult struct {
	MessageID string `json:"message_id"`
}

type AgentReplyParams struct {
	Caller    CallerInfo `json:"caller"`
	MessageID string     `json:"message_id"`
	Text      string     `json:"text"`
}

type AgentRespondParams struct {
	Caller      CallerInfo     `json:"caller"`
	NoticeID    string         `json:"notice_id"`
	Behavior    string         `json:"behavior,omitempty"` // "allow" | "deny"
	Reason      string         `json:"reason,omitempty"`
	OptionValue string         `json:"option_value,omitempty"` // a DecisionOption.Value from the notice
	Answers     map[string]any `json:"answers,omitempty"`      // question text → option label
}

type AgentOutputResult struct {
	Text string `json:"text"`
}

type AgentTranscriptParams struct {
	Caller    CallerInfo `json:"caller"`
	SessionID string     `json:"session_id"`
	Last      int        `json:"last,omitempty"` // 0 = all entries
}

type AgentTranscriptResult struct {
	Entries []transcript.Entry `json:"entries"`
}
