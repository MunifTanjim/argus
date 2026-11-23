package pi

import "github.com/MunifTanjim/argus/internal/session"

// cardUsage is pi's own usage report: the context its footer shows, the
// session's total tokens and its cost.
type cardUsage struct {
	hasContext bool
	tokens     int
	percent    float64
	total      int64
	hasCost    bool
	cost       float64
}

// newCardUsage keeps the context only when pi knows it: right after a
// compaction pi reports null until the next response.
func newCardUsage(tokens *int, percent *float64, cost *float64) *cardUsage {
	u := &cardUsage{}
	if tokens != nil && percent != nil {
		u.hasContext, u.tokens, u.percent = true, *tokens, *percent
	}
	if cost != nil {
		u.hasCost, u.cost = true, *cost
	}
	return u
}

func (u *cardUsage) summary() *session.Summary {
	return &session.Summary{HasContext: u.hasContext, ContextPct: u.percent, Tokens: u.tokens, TotalTokens: u.total}
}

// sessionStats is the slice of get_session_stats the card needs.
type sessionStats struct {
	Cost   *float64 `json:"cost"`
	Tokens *struct {
		Total int64 `json:"total"`
	} `json:"tokens"`
	ContextUsage *struct {
		Tokens  *int     `json:"tokens"`
		Percent *float64 `json:"percent"`
	} `json:"contextUsage"`
}

func (st sessionStats) usage() *cardUsage {
	if st.Cost == nil && st.ContextUsage == nil && st.Tokens == nil {
		return nil
	}
	var tokens *int
	var percent *float64
	if st.ContextUsage != nil {
		tokens, percent = st.ContextUsage.Tokens, st.ContextUsage.Percent
	}
	u := newCardUsage(tokens, percent, st.Cost)
	if st.Tokens != nil {
		u.total = st.Tokens.Total
	}
	return u
}
