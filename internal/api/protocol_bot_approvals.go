package api

import "time"

// Drafts: outward actions of unattended bot work, staged for the user.
const (
	MethodBotDraftSend    = "bot.draft.send"    // request: BotDraftParams; result: nil
	MethodBotDraftDiscard = "bot.draft.discard" // request: BotDraftParams; result: nil
)

// BotDraftParams names a draft: the thread and the id of its draft event.
type BotDraftParams struct {
	ThreadID string `json:"thread_id"`
	EventID  int64  `json:"event_id"`
}

// Approval activity.
const (
	MethodBotActivity = "bot.activity" // request: BotActivityParams; result: BotActivityResult
)

// BotActivityParams lists a bot's approval decisions, newest first.
// Unattended keeps only work the user did not start; Limit <= 0 means 50.
type BotActivityParams struct {
	BotID      string `json:"bot_id"`
	Unattended bool   `json:"unattended,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type BotActivityResult struct {
	Activity []BotActivity `json:"activity"`
}

// BotActivity is one decision. DecidedBy is floor, policy, hook, rule,
// grant, default, user or expiry; Hook is the policy hook's answer when it
// only observes.
type BotActivity struct {
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

// Bot secrets.
const (
	MethodBotSecretSet  = "bot.secret.set"  // request: BotSecretSetParams; result: nil
	MethodBotSecretList = "bot.secret.list" // request: BotRef; result: BotSecretListResult
	MethodBotSecretRm   = "bot.secret.rm"   // request: BotSecretRef; result: nil
)

// BotSecretSetParams stores Value under Name (an environment variable
// name) for the bot's sessions.
type BotSecretSetParams struct {
	BotID       string `json:"bot_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       string `json:"value"`
}

type BotSecretRef struct {
	BotID string `json:"bot_id"`
	Name  string `json:"name"`
}

type BotSecretListResult struct {
	Secrets []BotSecretInfo `json:"secrets"`
}

// BotSecretInfo describes a secret; its value is never sent.
type BotSecretInfo struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}
