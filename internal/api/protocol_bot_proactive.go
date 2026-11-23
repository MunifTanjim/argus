package api

import "time"

// Bot offers: proposals a bot makes in a chat thread, which the user
// starts or dismisses.
const (
	MethodBotOfferStart   = "bot.offer.start"   // request: BotOfferParams; result: BotSendResult (sends the offer's prompt)
	MethodBotOfferDismiss = "bot.offer.dismiss" // request: BotOfferParams; result: nil
)

// BotOfferParams names an offer by the thread its event is in.
type BotOfferParams struct {
	ThreadID string `json:"thread_id"`
	OfferID  string `json:"offer_id"`
}

// Bot usage and settings.
const (
	MethodBotUsage = "bot.usage" // request: BotUsageParams; result: BotUsageResult
	MethodBotSet   = "bot.set"   // request: BotSetParams; result: BotSetResult
)

// BotUsageParams asks for a bot's usage since Since (zero = all time).
type BotUsageParams struct {
	BotID string    `json:"bot_id"`
	Since time.Time `json:"since,omitempty"`
}

// BotUsageRow is the usage of the work one kind of starter began.
type BotUsageRow struct {
	StartedBy string  `json:"started_by"`
	Sessions  int64   `json:"sessions"`
	Cost      float64 `json:"cost"`
	Tokens    int64   `json:"tokens"`
}

// BotUsageResult is a bot's usage by started_by, and in total. Cost and
// tokens count only what the bot's CLI reports.
type BotUsageResult struct {
	BotID string        `json:"bot_id"`
	Since time.Time     `json:"since,omitempty"`
	Rows  []BotUsageRow `json:"rows"`
	Total BotUsageRow   `json:"total"`
}

// BotSetParams sets one setting in a bot's bot.yaml. Key is notify,
// proactivity, memory.mode, quiet-hours, brief.enabled or brief.at; an empty
// Value removes the setting, so the default applies.
type BotSetParams struct {
	BotID string `json:"bot_id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type BotSetResult struct {
	Commit string `json:"commit,omitempty"`
}

// BotSetting is one bot setting as it applies: Value is the resolved
// value, Explicit whether the bot's bot.yaml sets it (else the default).
type BotSetting struct {
	Value    string `json:"value"`
	Explicit bool   `json:"explicit,omitempty"`
}
