package api

// MethodBotTrust accepts changes made outside Argus to the files Argus checks
// before it commits in a bot's repository: git hooks and config, the bot's
// approval rules and approvals.yaml.
const MethodBotTrust = "bot.trust" // request: BotRef; result: BotTrustResult

// BotTrustResult lists the changes accepted, each "<file> (added|changed|removed)".
type BotTrustResult struct {
	Accepted []string `json:"accepted"`
}
