package api

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
