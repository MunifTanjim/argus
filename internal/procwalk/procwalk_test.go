package procwalk

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	out := "  PID  PPID ARGS\n  10     1 /bin/zsh -l\n\n  20    10 /Users/x/.opencode/bin/opencode serve --service\nx 1 bad\n"
	tbl := Parse(out)
	want := Proc{20, 10, "/Users/x/.opencode/bin/opencode serve --service"}
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

func pids(ps []Proc) []int {
	var out []int
	for _, p := range ps {
		out = append(out, p.PID)
	}
	return out
}

func TestAncestorsOrder(t *testing.T) {
	tbl := Table{
		1:  {1, 0, "init"},
		10: {10, 1, "a"},
		20: {20, 10, "b"},
		30: {30, 20, "c"},
	}
	if got := pids(tbl.Ancestors(30)); !slices.Equal(got, []int{20, 10, 1}) {
		t.Fatalf("got %v", got)
	}
}

func TestAncestorsCycleSafe(t *testing.T) {
	tbl := Table{5: {5, 6, "a"}, 6: {6, 5, "b"}}
	if got := tbl.Ancestors(5); len(got) > 2 {
		t.Fatalf("got %v", got)
	}
}

func TestAncestorsMissingPID(t *testing.T) {
	if got := (Table{}).Ancestors(999); got != nil {
		t.Fatalf("got %v", got)
	}
}
