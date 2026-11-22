package memory

import (
	"strings"
	"testing"
)

func TestParseRenderRoundTrip(t *testing.T) {
	content := "# Memory\n- prefers tabs (2026-01-02)\n- on call this week (2026-01-03) · until 2026-01-10\n- no date\n"
	entries := Parse(content)
	if len(entries) != 3 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Line != 2 || entries[0].Text != "prefers tabs" || entries[0].Date != "2026-01-02" {
		t.Fatalf("entry 0: %+v", entries[0])
	}
	if entries[1].Until != "2026-01-10" || entries[1].Text != "on call this week" {
		t.Fatalf("entry 1: %+v", entries[1])
	}
	if entries[2].Text != "no date" || entries[2].Date != "" {
		t.Fatalf("entry 2: %+v", entries[2])
	}
	if got := Render(entries, "# Memory"); got != content {
		t.Fatalf("round trip:\n%q\n%q", got, content)
	}
}

func TestApplyUpdateNeedsUniqueMatch(t *testing.T) {
	content := "# Memory\n- likes go (2026-01-01)\n- likes rust (2026-01-01)\n"
	out, applied, errs := Apply(content, []Op{{Action: "update", Match: "likes", Text: "likes zig"}}, "2026-02-01")
	if len(errs) != 1 || len(applied) != 0 || out != content {
		t.Fatalf("ambiguous: out=%q applied=%v errs=%v", out, applied, errs)
	}
	_, applied, errs = Apply(content, []Op{{Action: "update", Match: "nothing", Text: "x"}}, "2026-02-01")
	if len(errs) != 1 || len(applied) != 0 {
		t.Fatalf("missing: %v %v", applied, errs)
	}
	out, applied, errs = Apply(content, []Op{{Action: "update", Match: "rust", Text: "likes zig"}}, "2026-02-01")
	if len(errs) != 0 || len(applied) != 1 {
		t.Fatalf("unique: %v", errs)
	}
	if !strings.Contains(out, "- likes zig (2026-02-01)") || strings.Contains(out, "rust") {
		t.Fatalf("out=%q", out)
	}
	out, _, errs = Apply(out, []Op{{Action: "remove", Match: "zig"}}, "2026-02-01")
	if len(errs) != 0 || strings.Contains(out, "zig") {
		t.Fatalf("remove: %q %v", out, errs)
	}
}

func TestApplyRejectsDuplicate(t *testing.T) {
	content := "# Memory\n- Likes Go (2026-01-01)\n"
	out, applied, errs := Apply(content, []Op{
		{Action: "add", Text: "likes go"},
		{Action: "add", Text: "uses vim", Until: "2026-03-01"},
	}, "2026-02-01")
	if len(errs) != 1 || len(applied) != 1 || applied[0].Text != "uses vim" {
		t.Fatalf("applied=%v errs=%v", applied, errs)
	}
	if !strings.Contains(out, "- uses vim (2026-02-01) · until 2026-03-01\n") {
		t.Fatalf("out=%q", out)
	}
}

func entries(texts ...string) []Entry {
	var es []Entry
	for i, t := range texts {
		es = append(es, Entry{Line: i + 1, Text: t})
	}
	return es
}

func TestCheckTidyFraction(t *testing.T) {
	before := entries("a1", "b2", "c3", "d4", "e5", "f6", "g7", "h8", "i9", "j10")
	if err := CheckTidy(before, entries("b2", "c3", "d4", "e5", "f6", "g7", "h8", "i9", "j10"), nil); err != nil {
		t.Fatalf("1 of 10 removed: %v", err)
	}
	if err := CheckTidy(before, entries("c3", "d4", "e5", "f6", "g7", "h8", "i9", "j10"), nil); err != nil {
		t.Fatalf("2 of 10 removed: %v", err)
	}
	if err := CheckTidy(before, entries("d4", "e5", "f6", "g7", "h8", "i9", "j10"), nil); err == nil {
		t.Fatal("3 of 10 removed should fail")
	}
	small := entries("a", "b", "c")
	if err := CheckTidy(small, entries("b", "c"), nil); err != nil {
		t.Fatalf("at least one change allowed: %v", err)
	}
	if err := CheckTidy(small, entries("c"), nil); err == nil {
		t.Fatal("2 of 3 should fail")
	}
	// additions do not count
	if err := CheckTidy(before, append(entries("a1", "b2", "c3", "d4", "e5", "f6", "g7", "h8", "i9", "j10"), Entry{Text: "new detail"}), nil); err != nil {
		t.Fatalf("additions: %v", err)
	}
}

func TestCheckTidyProtectsUserLines(t *testing.T) {
	before := entries("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	after := entries("a", "c", "d", "e", "f", "g", "h", "i", "j")
	if err := CheckTidy(before, after, map[int]bool{2: true}); err == nil {
		t.Fatal("removing user line should fail")
	}
	if err := CheckTidy(before, after, map[int]bool{3: true}); err != nil {
		t.Fatalf("other user line: %v", err)
	}
}

func TestCheckTidyNoNewWords(t *testing.T) {
	before := entries("prefers tabs (not spaces)", "uses vim daily", "x", "y", "z", "p", "q", "r", "s", "t")
	ok := append(entries("x", "y", "z", "p", "q", "r", "s", "t"), Entry{Text: "Uses vim daily, prefers tabs"})
	if err := CheckTidy(before, ok, nil); err != nil {
		t.Fatalf("merge with known words: %v", err)
	}
	bad := append(entries("x", "y", "z", "p", "q", "r", "s", "t"), Entry{Text: "uses neovim daily", Date: "2026-01-01"})
	if err := CheckTidy(before, bad, nil); err == nil {
		t.Fatal("new word should fail")
	}
}
