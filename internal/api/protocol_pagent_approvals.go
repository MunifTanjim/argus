package api

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
