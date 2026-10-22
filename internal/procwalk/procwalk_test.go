package procwalk

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	out := "  PID  PPID ARGS\n  10     1 /bin/zsh -l\n\n  20    10 /Users/x/.opencode/bin/opencode serve --service\nx 1 bad\n"
	tbl := Parse(out)
	want := Proc{PID: 20, PPID: 10, Args: "/Users/x/.opencode/bin/opencode serve --service"}
	if tbl[20] != want {
		t.Fatalf("tbl[20] = %+v, want %+v", tbl[20], want)
	}
	if len(tbl) != 2 {
		t.Fatalf("len = %d, want 2", len(tbl))
	}
}

func TestName(t *testing.T) {
	cases := map[string]string{
		"/Users/x/.codex/.../bin/codex app-server --listen unix://": "codex",
		"agy": "agy",
		"":    "",
	}
	for args, want := range cases {
		if got := (Proc{Args: args}).Name(); got != want {
			t.Errorf("Name(%q) = %q, want %q", args, got, want)
		}
	}
}

func TestParseExec(t *testing.T) {
	tbl := Parse("10 1 /bin/zsh -l\n20 10 opencode serve\n")
	tbl.ParseExec("   10 zsh\n   20 Google Chrome He\n   30 orphan\n")
	if tbl[10].Exec != "zsh" || tbl[20].Exec != "Google Chrome He" {
		t.Fatalf("exec = %q, %q", tbl[10].Exec, tbl[20].Exec)
	}
	if _, ok := tbl[30]; ok {
		t.Fatal("ParseExec added a process missing from the table")
	}
}

func TestRuns(t *testing.T) {
	cases := []struct {
		p    Proc
		want bool
	}{
		{Proc{Args: "/x/bin/opencode serve"}, true},
		{Proc{Args: "/x/node_modules/opencode-ai/bin/opencode.exe serve"}, true},
		{Proc{Args: "/Users/x/Application Support/tools/opencode serve", Exec: "opencode"}, true},
		{Proc{Args: "renamed title", Exec: "opencode.exe"}, true},
		{Proc{Args: "node /x/bin/opencode", Exec: "node"}, false},
		{Proc{Args: "/x/bin/opencode-helper", Exec: "opencode-helper"}, false},
		{Proc{}, false},
	}
	for _, c := range cases {
		if got := c.p.Runs("opencode"); got != c.want {
			t.Errorf("Runs(%+v) = %v, want %v", c.p, got, c.want)
		}
	}
}

func pids(ps []Proc) []int {
	var out []int
	for _, p := range ps {
		out = append(out, p.PID)
	}
	return out
}

func TestAncestorsOrder(t *testing.T) {
	tbl := Table{
		1:  {PID: 1, PPID: 0, Args: "init"},
		10: {PID: 10, PPID: 1, Args: "a"},
		20: {PID: 20, PPID: 10, Args: "b"},
		30: {PID: 30, PPID: 20, Args: "c"},
	}
	if got := pids(tbl.Ancestors(30)); !slices.Equal(got, []int{20, 10, 1}) {
		t.Fatalf("got %v", got)
	}
}

func TestAncestorsCycleSafe(t *testing.T) {
	tbl := Table{5: {PID: 5, PPID: 6, Args: "a"}, 6: {PID: 6, PPID: 5, Args: "b"}}
	if got := tbl.Ancestors(5); len(got) > 2 {
		t.Fatalf("got %v", got)
	}
}

func TestAncestorsMissingPID(t *testing.T) {
	if got := (Table{}).Ancestors(999); got != nil {
		t.Fatalf("got %v", got)
	}
}
