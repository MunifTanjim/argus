package api

import "time"

// Agent offers: proposals an agent makes in a chat thread, which the user
// starts or dismisses.
const (
	MethodPAgentOfferStart   = "pagent.offer.start"   // request: PAgentOfferParams; result: PAgentSendResult (sends the offer's prompt)
	MethodPAgentOfferDismiss = "pagent.offer.dismiss" // request: PAgentOfferParams; result: nil
)

// PAgentOfferParams names an offer by the thread its event is in.
type PAgentOfferParams struct {
	ThreadID string `json:"thread_id"`
	OfferID  string `json:"offer_id"`
}

// Agent usage and settings.
const (
	MethodPAgentUsage = "pagent.usage" // request: PAgentUsageParams; result: PAgentUsageResult
	MethodPAgentSet   = "pagent.set"   // request: PAgentSetParams; result: PAgentSetResult
)

// PAgentUsageParams asks for an agent's usage since Since (zero = all time).
type PAgentUsageParams struct {
	AgentID string    `json:"agent_id"`
	Since   time.Time `json:"since,omitempty"`
}

// PAgentUsageRow is the usage of the work one kind of starter began.
type PAgentUsageRow struct {
	StartedBy string  `json:"started_by"`
	Sessions  int64   `json:"sessions"`
	Cost      float64 `json:"cost"`
	Tokens    int64   `json:"tokens"`
}

// PAgentUsageResult is an agent's usage by started_by, and in total. Cost and
// tokens count only what the agent's CLI reports.
type PAgentUsageResult struct {
	AgentID string           `json:"agent_id"`
	Since   time.Time        `json:"since,omitempty"`
	Rows    []PAgentUsageRow `json:"rows"`
	Total   PAgentUsageRow   `json:"total"`
}

// PAgentSetParams sets one setting in an agent's agent.yaml. Key is notify,
// proactivity, memory.mode, quiet-hours, brief.enabled or brief.at; an empty
// Value removes the setting, so the default applies.
type PAgentSetParams struct {
	AgentID string `json:"agent_id"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

type PAgentSetResult struct {
	Commit string `json:"commit,omitempty"`
}
