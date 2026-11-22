package policy

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Outcomes of a decision.
const (
	OutcomeAllow = "allow"
	OutcomeAsk   = "ask"
	OutcomeDeny  = "deny"
	OutcomeDraft = "draft"
)

// Rule is one approval rule from agent.yaml or the node's approvals.yaml.
// Empty fields match anything.
type Rule struct {
	// Tool is a glob over the tool name.
	Tool string `yaml:"tool,omitempty"`
	// Target is a glob over the action target. For shell tools it matches the
	// program, or the program with its arguments when the pattern has a space
	// ("gh pr *").
	Target string `yaml:"target,omitempty"`
	// Class matches actions of this class (outward, irreversible, security).
	Class string `yaml:"class,omitempty"`
	// StartedBy limits the rule to work started this way.
	StartedBy []string `yaml:"started-by,omitempty"`
	// Decision is allow, ask or deny.
	Decision string `yaml:"decision"`
	// Expires ends the rule at that time. Zero means it does not expire.
	Expires time.Time `yaml:"expires,omitempty"`
	Note    string    `yaml:"note,omitempty"`
}

// Validate reports whether the rule can be evaluated.
func (r Rule) Validate() error {
	switch r.Decision {
	case OutcomeAllow, OutcomeAsk, OutcomeDeny:
	default:
		return fmt.Errorf("approval rule decision %q: want allow, ask or deny", r.Decision)
	}
	switch r.Class {
	case "", ClassOutward, ClassIrreversible, ClassSecurity:
	default:
		return fmt.Errorf("approval rule class %q: want outward, irreversible or security", r.Class)
	}
	return nil
}

// Expired reports whether the rule has ended at now.
func (r Rule) Expired(now time.Time) bool {
	return !r.Expires.IsZero() && !now.Before(r.Expires)
}

// String summarizes the rule for decisions and logs.
func (r Rule) String() string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("tool", r.Tool)
	add("target", r.Target)
	add("class", r.Class)
	if len(r.StartedBy) > 0 {
		add("started-by", strings.Join(r.StartedBy, ","))
	}
	add("decision", r.Decision)
	return strings.Join(parts, " ")
}

// glob matches s against pattern, where * matches any run of characters
// (including /) and ? matches one character.
func glob(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case star >= 0:
			mark++
			ti, pi = mark, star+1
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// matches reports whether the rule applies to the action. Shell chains are
// matched by every program: an allow rule needs all of them to match, deny and
// ask rules need one.
func (r Rule) matches(a Action, startedBy string) bool {
	if r.Tool != "" && !glob(r.Tool, a.Tool) {
		return false
	}
	if r.Class != "" && !slices.Contains(a.Class, r.Class) {
		return false
	}
	if len(r.StartedBy) > 0 && !slices.Contains(r.StartedBy, startedBy) {
		return false
	}
	if r.Target == "" {
		return true
	}
	if kindOf(a.Tool) == kindShell {
		if cmds := shellCommands(a.Input); len(cmds) > 0 {
			match := func(c command) bool {
				return glob(r.Target, c.prog) || strings.Contains(r.Target, " ") && glob(r.Target, c.String())
			}
			if r.Decision == OutcomeAllow {
				return slices.IndexFunc(cmds, func(c command) bool { return !match(c) }) < 0
			}
			return slices.IndexFunc(cmds, match) >= 0
		}
	}
	return glob(r.Target, a.Target)
}

// DenyKey identifies the exact operation, for the thread's denied set.
func (a Action) DenyKey() string {
	return a.Tool + "\x00" + strings.TrimSpace(string(a.Input))
}

// GrantKey identifies what "allow in this thread" covers: the tool and target,
// narrowed to the programs of a shell chain and to the classes of the action,
// so a grant for `git status` does not cover `git status && git push`.
func (a Action) GrantKey() string {
	target := a.Target
	if kindOf(a.Tool) == kindShell {
		var progs []string
		for _, c := range shellCommands(a.Input) {
			if !slices.Contains(progs, c.prog) {
				progs = append(progs, c.prog)
			}
		}
		slices.Sort(progs)
		target = strings.Join(progs, " ")
	}
	return a.Tool + "\x00" + target + "\x00" + strings.Join(a.Class, ",")
}
