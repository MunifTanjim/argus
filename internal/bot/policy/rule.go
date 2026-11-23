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

// Rule is one approval rule from bot.yaml or the node's approvals.yaml.
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

// matches reports whether the rule applies to the action. Shell chains and
// multi-file edits are matched by every program or path: an allow rule needs
// all of them to match, deny and ask rules need one. An allow rule never
// matches a shell line with a command the parser cannot read.
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
	allow := r.Decision == OutcomeAllow
	switch kindOf(a.Tool) {
	case kindShell:
		cmds := shellCommands(a.Input)
		if allow && slices.ContainsFunc(cmds, func(c command) bool { return c.opaque }) {
			return false
		}
		if r.Target == "" {
			return true
		}
		if len(cmds) > 0 {
			match := func(c command) bool {
				return glob(r.Target, c.prog) || strings.Contains(r.Target, " ") && glob(r.Target, c.String())
			}
			return matchAll(cmds, match, allow)
		}
	case kindFile:
		if r.Target == "" {
			return true
		}
		paths := filePaths(a.Tool, a.Input)
		if len(paths) == 0 {
			paths = []string{a.Target}
		}
		return matchAll(paths, func(p string) bool { return pathMatches(r.Target, p) }, allow)
	}
	return r.Target == "" || glob(r.Target, a.Target)
}

// matchAll reports whether every item matches when all is set, else whether
// one does.
func matchAll[T any](items []T, match func(T) bool, all bool) bool {
	if all {
		return !slices.ContainsFunc(items, func(x T) bool { return !match(x) })
	}
	return slices.ContainsFunc(items, match)
}

func climbs(p string) bool { return p == ".." || strings.HasPrefix(p, "../") }

// pathMatches matches a file path against a target pattern after cleaning
// both. A relative pattern never matches a path that climbs out of the
// working directory with "..".
func pathMatches(pattern, p string) bool {
	if p == "" {
		return false
	}
	cp, cpat := cleanPath(p), cleanPath(pattern)
	if climbs(cp) && !climbs(cpat) && !strings.HasPrefix(cpat, "/") && !strings.HasPrefix(cpat, "~") {
		return false
	}
	return glob(cpat, cp)
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
	} else if kindOf(a.Tool) == kindFile {
		var paths []string
		for _, p := range filePaths(a.Tool, a.Input) {
			if p = cleanPath(p); !slices.Contains(paths, p) {
				paths = append(paths, p)
			}
		}
		if len(paths) > 0 {
			slices.Sort(paths)
			target = strings.Join(paths, "\x00")
		}
	}
	return a.Tool + "\x00" + target + "\x00" + strings.Join(a.Class, ",")
}
