package policy

import (
	"encoding/json"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

var now = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func bash(cmd string) Action { return Describe("claude", "Bash", bashIn(cmd)) }

func in(started string) DecideInput { return DecideInput{StartedBy: started, Now: now} }

func check(t *testing.T, name string, got Decision, outcome, by string) {
	t.Helper()
	if got.Outcome != outcome || got.By != by {
		t.Errorf("%s: got %s/%s (%s), want %s/%s", name, got.Outcome, got.By, got.Reason, outcome, by)
	}
}

func TestDefaults(t *testing.T) {
	check(t, "you ordinary", Decide(bash("ls"), in("you")), OutcomeAsk, ByDefault)
	check(t, "you irreversible", Decide(bash("rm -rf x"), in("you")), OutcomeAsk, ByDefault)
	check(t, "you outward", Decide(bash("git push"), in("you")), OutcomeAsk, ByDefault)
	check(t, "you security", Decide(bash("ssh-keygen"), in("you")), OutcomeAsk, ByDefault)
	for _, s := range []string{"routine", "check-back", "event", "handoff", ""} {
		d := Decide(bash("ls"), in(s))
		check(t, "unattended "+s, d, OutcomeDeny, ByDefault)
		if d.Reason != ReasonUnattended {
			t.Errorf("reason %q", d.Reason)
		}
	}
}

func TestStartedByFallsBackToAction(t *testing.T) {
	a := bash("ls")
	a.StartedBy = "you"
	check(t, "action started-by", Decide(a, DecideInput{Now: now}), OutcomeAsk, ByDefault)
}

func TestFloorOutwardUnattendedDrafts(t *testing.T) {
	d := Decide(bash("cd x && git push"), in("routine"))
	check(t, "draft", d, OutcomeDraft, ByFloor)
	if d.Reason != ReasonDraft {
		t.Errorf("reason %q", d.Reason)
	}
}

func TestRuleCannotOverrideFloor(t *testing.T) {
	allow := []Rule{{Tool: "Bash", Decision: OutcomeAllow}, {Class: ClassOutward, Decision: OutcomeAllow}}
	i := in("routine")
	i.AgentRules, i.GlobalRules = allow, allow
	i.Hook = &HookResult{Decision: OutcomeAllow, Mode: "fail-open"}
	i.Execution = ExecutionAllow
	check(t, "outward", Decide(bash("git push"), i), OutcomeDraft, ByFloor)

	i = in("you")
	i.AgentRules = allow
	i.Denied = map[string]struct{}{bash("rm x").DenyKey(): {}}
	i.Grants = map[string]struct{}{bash("rm x").GrantKey(): {}}
	i.Hook = &HookResult{Decision: OutcomeAllow, Mode: "enforce"}
	check(t, "repeat", Decide(bash("rm x"), i), OutcomeDeny, ByFloor)
}

func TestFloorOrder(t *testing.T) {
	i := in("routine")
	i.Denied = map[string]struct{}{bash("git push").DenyKey(): {}}
	check(t, "draft before repeat", Decide(bash("git push"), i), OutcomeDraft, ByFloor)
}

func TestDenyRepeatIsExact(t *testing.T) {
	i := in("you")
	i.Denied = map[string]struct{}{bash("rm a").DenyKey(): {}}
	check(t, "same", Decide(bash("rm a"), i), OutcomeDeny, ByFloor)
	check(t, "other", Decide(bash("rm b"), i), OutcomeAsk, ByDefault)
	check(t, "other tool", Decide(Describe("c", "Write", json.RawMessage(`{"file_path":"a"}`)), i), OutcomeAsk, ByDefault)
}

func TestExecutionPolicy(t *testing.T) {
	i := in("routine")
	i.Execution = ExecutionNeverUnattended
	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeAllow}}
	check(t, "never unattended", Decide(bash("ls"), i), OutcomeDeny, ByPolicy)

	i.StartedBy = "you"
	check(t, "never-unattended spares you", Decide(bash("ls"), i), OutcomeAllow, ByRule)

	i = in("routine")
	i.Execution = ExecutionAsk
	check(t, "ask unattended no rule", Decide(bash("ls"), i), OutcomeDeny, ByDefault)

	i = in("routine")
	i.Execution = ExecutionAllow
	check(t, "allow unattended", Decide(bash("ls"), i), OutcomeAllow, ByPolicy)
	i = in("you")
	i.Execution = ExecutionAllow
	check(t, "allow you ordinary", Decide(bash("ls"), i), OutcomeAllow, ByPolicy)
	check(t, "allow you classed", Decide(bash("rm -rf x"), i), OutcomeAsk, ByDefault)
}

func TestHook(t *testing.T) {
	deny := []Rule{{Tool: "Bash", Decision: OutcomeDeny}}
	allow := []Rule{{Tool: "Bash", Decision: OutcomeAllow}}
	for _, dec := range []string{OutcomeAllow, OutcomeAsk, OutcomeDeny} {
		i := in("you")
		i.AgentRules = deny
		i.Hook = &HookResult{Decision: dec, Mode: "fail-closed", Reason: "r"}
		d := Decide(bash("ls"), i)
		check(t, "enforce "+dec, d, dec, ByHook)
		if d.Reason != "r" {
			t.Errorf("reason %q", d.Reason)
		}
	}
	i := in("you")
	i.AgentRules = allow
	i.Hook = &HookResult{Decision: OutcomeDeny, Mode: HookShadow}
	check(t, "shadow ignored", Decide(bash("ls"), i), OutcomeAllow, ByRule)

	i.Hook = &HookResult{Decision: "bogus", Mode: "fail-open"}
	check(t, "invalid hook decision ignored", Decide(bash("ls"), i), OutcomeAllow, ByRule)

	i = in("routine")
	i.Execution = ExecutionNeverUnattended
	i.Hook = &HookResult{Decision: OutcomeAllow, Mode: "fail-open"}
	check(t, "policy before hook", Decide(bash("ls"), i), OutcomeDeny, ByPolicy)
}

func TestRulePrecedence(t *testing.T) {
	allow := Rule{Tool: "Bash", Target: "git", Decision: OutcomeAllow}
	ask := Rule{Tool: "Bash", Target: "git", Decision: OutcomeAsk}
	deny := Rule{Tool: "Bash", Target: "git", Decision: OutcomeDeny, Note: "no git"}

	i := in("you")
	i.AgentRules = []Rule{allow}
	i.GlobalRules = []Rule{ask}
	check(t, "ask beats allow", Decide(bash("git status"), i), OutcomeAsk, ByRule)

	i.AgentRules = []Rule{allow, deny}
	d := Decide(bash("git status"), i)
	check(t, "deny beats allow", d, OutcomeDeny, ByRule)
	if d.Reason != "no git" || d.Rule == "" {
		t.Errorf("reason %q rule %q", d.Reason, d.Rule)
	}

	i.AgentRules = []Rule{allow}
	i.GlobalRules = []Rule{deny}
	check(t, "global deny beats agent allow", Decide(bash("git status"), i), OutcomeDeny, ByRule)

	i.AgentRules, i.GlobalRules = nil, []Rule{allow}
	check(t, "global allow", Decide(bash("git status"), i), OutcomeAllow, ByRule)

	i = in("routine")
	i.AgentRules = []Rule{allow}
	check(t, "unattended allow by rule", Decide(bash("git status"), i), OutcomeAllow, ByRule)
	i.AgentRules = []Rule{ask}
	check(t, "unattended ask by rule", Decide(bash("git status"), i), OutcomeAsk, ByRule)
}

func TestRuleExpiry(t *testing.T) {
	i := in("routine")
	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeAllow, Expires: now.Add(-time.Second)}}
	check(t, "expired", Decide(bash("ls"), i), OutcomeDeny, ByDefault)
	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeAllow, Expires: now}}
	check(t, "expires now", Decide(bash("ls"), i), OutcomeDeny, ByDefault)
	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeAllow, Expires: now.Add(time.Hour)}}
	check(t, "live", Decide(bash("ls"), i), OutcomeAllow, ByRule)

	i = in("you")
	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeDeny, Expires: now.Add(-time.Hour)}}
	check(t, "expired deny", Decide(bash("ls"), i), OutcomeAsk, ByDefault)
}

func TestInvalidRuleIgnored(t *testing.T) {
	i := in("you")
	i.AgentRules = []Rule{{Tool: "Bash", Decision: "bogus"}, {Class: "weird", Decision: OutcomeDeny}}
	check(t, "invalid", Decide(bash("ls"), i), OutcomeAsk, ByDefault)
}

func TestRuleMatching(t *testing.T) {
	file := func(p string) Action { return Describe("c", "Edit", json.RawMessage(`{"file_path":"`+p+`"}`)) }
	mcp := Describe("c", "mcp__gmail__list_threads", json.RawMessage(`{}`))
	tests := []struct {
		name    string
		rule    Rule
		a       Action
		started string
		want    bool
	}{
		{"all empty", Rule{Decision: "allow"}, bash("ls"), "you", true},
		{"tool exact", Rule{Tool: "Bash"}, bash("ls"), "you", true},
		{"tool other", Rule{Tool: "Edit"}, bash("ls"), "you", false},
		{"tool glob", Rule{Tool: "mcp__gmail__*"}, mcp, "you", true},
		{"tool glob other server", Rule{Tool: "mcp__slack__*"}, mcp, "you", false},
		{"target prog", Rule{Target: "git"}, bash("git status"), "you", true},
		{"target prog other", Rule{Target: "git"}, bash("gh pr list"), "you", false},
		{"target subcommand", Rule{Target: "gh pr *"}, bash("gh pr create --fill"), "you", true},
		{"target subcommand miss", Rule{Target: "gh pr *"}, bash("gh issue list"), "you", false},
		{"target glob prog", Rule{Target: "g*"}, bash("git x"), "you", true},
		{"target path glob", Rule{Target: "/repo/*"}, file("/repo/a/b.go"), "you", true},
		{"target path glob miss", Rule{Target: "/repo/*"}, file("/other/a.go"), "you", false},
		{"target mcp tool", Rule{Target: "list_*"}, mcp, "you", true},
		{"class", Rule{Class: ClassOutward}, bash("git push"), "you", true},
		{"class miss", Rule{Class: ClassOutward}, bash("git status"), "you", false},
		{"started-by hit", Rule{StartedBy: []string{"routine", "event"}}, bash("ls"), "event", true},
		{"started-by miss", Rule{StartedBy: []string{"routine", "event"}}, bash("ls"), "you", false},
		{"chain ask any", Rule{Target: "git", Decision: "ask"}, bash("cd x && git push"), "you", true},
		{"chain allow needs all", Rule{Target: "cd", Decision: "allow"}, bash("cd x && git push"), "you", false},
		{"chain allow all", Rule{Target: "git", Decision: "allow"}, bash("git add . && git commit -m x"), "you", true},
		{"wrapper stripped", Rule{Target: "git", Decision: "allow"}, bash("sudo git status"), "you", true},
	}
	for _, tt := range tests {
		if got := tt.rule.matches(tt.a, tt.started); got != tt.want {
			t.Errorf("%s: got %v", tt.name, got)
		}
	}
}

func TestAllowRuleDoesNotCoverChain(t *testing.T) {
	i := in("you")
	i.AgentRules = []Rule{{Tool: "Bash", Target: "cd", Decision: OutcomeAllow}}
	check(t, "cd && rm", Decide(bash("cd x && rm -rf y"), i), OutcomeAsk, ByDefault)
}

func TestGrants(t *testing.T) {
	i := in("you")
	i.Grants = map[string]struct{}{bash("git status").GrantKey(): {}}
	check(t, "granted", Decide(bash("git status -s"), i), OutcomeAllow, ByGrant)
	check(t, "chain not covered", Decide(bash("git status && git push"), i), OutcomeAsk, ByDefault)
	check(t, "other program", Decide(bash("ls"), i), OutcomeAsk, ByDefault)

	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeAsk}}
	check(t, "grant beats ask rule", Decide(bash("git status"), i), OutcomeAllow, ByGrant)
	i.AgentRules = []Rule{{Tool: "Bash", Decision: OutcomeDeny}}
	check(t, "deny rule beats grant", Decide(bash("git status"), i), OutcomeDeny, ByRule)
}

func TestRuleYAML(t *testing.T) {
	src := `
approvals:
  - { tool: Bash, target: git, decision: allow }
  - { tool: "mcp__gmail__*", decision: ask, started-by: [routine, event] }
  - { class: irreversible, decision: deny, expires: 2026-12-01T00:00:00Z, note: careful }
`
	var v struct {
		Approvals []Rule `yaml:"approvals"`
	}
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Approvals) != 3 {
		t.Fatalf("got %d rules", len(v.Approvals))
	}
	r := v.Approvals[1]
	if r.Tool != "mcp__gmail__*" || r.Decision != "ask" || len(r.StartedBy) != 2 || r.StartedBy[1] != "event" {
		t.Errorf("rule 1: %+v", r)
	}
	r = v.Approvals[2]
	if r.Class != ClassIrreversible || r.Note != "careful" || !r.Expires.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("rule 2: %+v", r)
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Approvals []Rule `yaml:"approvals"`
	}
	if err := yaml.Unmarshal(out, &back); err != nil || len(back.Approvals) != 3 || !back.Approvals[2].Expires.Equal(r.Expires) {
		t.Errorf("round trip: %v %s", err, out)
	}
}

func TestGlob(t *testing.T) {
	tests := []struct {
		p, s string
		want bool
	}{
		{"git", "git", true}, {"git", "gits", false}, {"*", "", true}, {"*", "a/b", true},
		{"a*c", "abbc", true}, {"a*c", "abbd", false}, {"a?c", "abc", true}, {"a?c", "ac", false},
		{"gh pr *", "gh pr create", true}, {"*x*", "axb", true}, {"a**", "a", true},
	}
	for _, tt := range tests {
		if glob(tt.p, tt.s) != tt.want {
			t.Errorf("glob(%q,%q) != %v", tt.p, tt.s, tt.want)
		}
	}
}
