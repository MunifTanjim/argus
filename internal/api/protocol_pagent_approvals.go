package api

import "time"

// Drafts: outward actions of unattended agent work, staged for the user.
const (
	MethodPAgentDraftSend    = "pagent.draft.send"    // request: PAgentDraftParams; result: nil
	MethodPAgentDraftDiscard = "pagent.draft.discard" // request: PAgentDraftParams; result: nil
)

// PAgentDraftParams names a draft: the thread and the id of its draft event.
type PAgentDraftParams struct {
	ThreadID string `json:"thread_id"`
	EventID  int64  `json:"event_id"`
}

// Approval activity.
const (
	MethodPAgentActivity = "pagent.activity" // request: PAgentActivityParams; result: PAgentActivityResult
)

// PAgentActivityParams lists an agent's approval decisions, newest first.
// Unattended keeps only work the user did not start; Limit <= 0 means 50.
type PAgentActivityParams struct {
	AgentID    string `json:"agent_id"`
	Unattended bool   `json:"unattended,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type PAgentActivityResult struct {
	Activity []PAgentActivity `json:"activity"`
}

// PAgentActivity is one decision. DecidedBy is floor, policy, hook, rule,
// grant, default, user or expiry; Hook is the policy hook's answer when it
// only observes.
type PAgentActivity struct {
	ID        int64     `json:"id"`
	ThreadID  string    `json:"thread_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	StartedBy string    `json:"started_by"`
	Tool      string    `json:"tool"`
	Target    string    `json:"target,omitempty"`
	Class     []string  `json:"class,omitempty"`
	Decision  string    `json:"decision"`
	DecidedBy string    `json:"decided_by"`
	Rule      string    `json:"rule,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Hook      string    `json:"hook,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
