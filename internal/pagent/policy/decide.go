package policy

import "time"

// Execution policies of a node (agents.execution).
const (
	ExecutionAsk             = "ask"
	ExecutionAllow           = "allow"
	ExecutionNeverUnattended = "never-unattended"
)

// Who made a decision.
const (
	ByFloor   = "floor"
	ByPolicy  = "policy"
	ByHook    = "hook"
	ByRule    = "rule"
	ByGrant   = "grant"
	ByDefault = "default"
)

// StartedByYou marks work started by a user message. Every other value, and an
// empty one, is unattended.
const StartedByYou = "you"

// Hook modes. Only shadow decisions are ignored by Decide.
const HookShadow = "shadow"

// HookResult is the policy hook's answer, computed by the caller. Errors and
// timeouts are resolved by the caller from the hook's mode: fail-closed turns
// them into a deny result, fail-open leaves the hook out.
type HookResult struct {
	Decision string
	Mode     string
	Reason   string
}

// DecideInput is everything Decide needs besides the action.
type DecideInput struct {
	StartedBy string
	// Execution is the node's execution policy; empty means ask.
	Execution string
	Hook      *HookResult
	// AgentRules and GlobalRules are evaluated together.
	AgentRules  []Rule
	GlobalRules []Rule
	// Denied holds DenyKey values the user denied in this thread since their
	// last message.
	Denied map[string]struct{}
	// Grants holds GrantKey values of "allow in this thread" answers.
	Grants map[string]struct{}
	Now    time.Time
}

// Decision is the outcome of Decide.
type Decision struct {
	Outcome string
	By      string
	Rule    string
	Reason  string
}

// Reasons shown to the model or the user.
const (
	ReasonDraft      = "staged as a draft for the user; do not retry"
	ReasonRepeat     = "the user denied this earlier in this thread"
	ReasonNever      = "this node does not allow unattended agent work to run actions"
	ReasonUnattended = "not allowed for unattended work — add an approval rule to allow it"
	ReasonProtected  = "changes the agent's approval rules, git hooks or settings — the user must approve it"
)

// Decide applies the approval order: floors, the node's execution policy, the
// policy hook, rules (deny, then ask, then allow) and defaults. A rule, hook or
// policy never overrides a floor.
func Decide(a Action, in DecideInput) Decision {
	started := in.StartedBy
	if started == "" {
		started = a.StartedBy
	}
	unattended := started != StartedByYou
	// Protected writes are never allowed by a hook, rule, grant or the
	// execution policy.
	protected := protectedWrite(a)
	classes := a.Class
	if len(classes) == 0 {
		classes = Classify(a)
		a.Class = classes
	}

	// 1. Floors.
	if unattended && hasClass(classes, ClassOutward) {
		return Decision{Outcome: OutcomeDraft, By: ByFloor, Reason: ReasonDraft}
	}
	if _, ok := in.Denied[a.DenyKey()]; ok {
		return Decision{Outcome: OutcomeDeny, By: ByFloor, Reason: ReasonRepeat}
	}

	// 2. Node execution policy.
	if in.Execution == ExecutionNeverUnattended && unattended {
		return Decision{Outcome: OutcomeDeny, By: ByPolicy, Reason: ReasonNever}
	}

	// 3. Policy hook, in any mode but shadow.
	if h := in.Hook; h != nil && h.Mode != HookShadow {
		switch h.Decision {
		case OutcomeAllow:
			if !protected {
				return Decision{Outcome: h.Decision, By: ByHook, Reason: h.Reason}
			}
		case OutcomeAsk, OutcomeDeny:
			return Decision{Outcome: h.Decision, By: ByHook, Reason: h.Reason}
		}
	}

	// 4. Rules: deny > ask > allow. A thread grant stands in for an allow
	// answer to an ask, so it beats ask rules but not deny rules.
	best, bestRule := "", Rule{}
	rank := map[string]int{OutcomeAllow: 1, OutcomeAsk: 2, OutcomeDeny: 3}
	for _, rules := range [][]Rule{in.AgentRules, in.GlobalRules} {
		for _, r := range rules {
			if r.Validate() != nil || r.Expired(in.Now) || protected && r.Decision == OutcomeAllow || !r.matches(a, started) {
				continue
			}
			if rank[r.Decision] > rank[best] {
				best, bestRule = r.Decision, r
			}
		}
	}
	if best == OutcomeDeny {
		return Decision{Outcome: OutcomeDeny, By: ByRule, Rule: bestRule.String(), Reason: noteOr(bestRule, "denied by an approval rule")}
	}
	if _, ok := in.Grants[a.GrantKey()]; ok && !protected {
		return Decision{Outcome: OutcomeAllow, By: ByGrant, Reason: "allowed for this thread"}
	}
	if best != "" {
		return Decision{Outcome: best, By: ByRule, Rule: bestRule.String(), Reason: noteOr(bestRule, "")}
	}

	// 5. Defaults.
	if protected {
		if unattended {
			return Decision{Outcome: OutcomeDeny, By: ByDefault, Reason: ReasonProtected}
		}
		return Decision{Outcome: OutcomeAsk, By: ByDefault, Reason: ReasonProtected}
	}
	classed := len(classes) > 0
	switch {
	case in.Execution == ExecutionAllow && (unattended || !classed):
		return Decision{Outcome: OutcomeAllow, By: ByPolicy}
	case unattended:
		return Decision{Outcome: OutcomeDeny, By: ByDefault, Reason: ReasonUnattended}
	default:
		return Decision{Outcome: OutcomeAsk, By: ByDefault}
	}
}

func hasClass(classes []string, c string) bool {
	for _, x := range classes {
		if x == c {
			return true
		}
	}
	return false
}

func noteOr(r Rule, def string) string {
	if r.Note != "" {
		return r.Note
	}
	return def
}
