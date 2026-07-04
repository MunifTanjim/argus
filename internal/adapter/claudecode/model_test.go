package claudecode

import "testing"

func TestModelDisplayName(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-8":            "Opus 4.8",
		"claude-sonnet-5":            "Sonnet 5",
		"claude-haiku-4-5":           "Haiku 4.5",
		"claude-fable-5":             "Fable 5",
		"claude-opus-4-7-20260201":   "Opus 4.7",   // date stamp dropped
		"claude-sonnet-4-20250514":   "Sonnet 4",   // date stamp dropped
		"claude-sonnet-4-5-20250514": "Sonnet 4.5", // patch kept, date dropped
		"claude-3-5-sonnet":          "Sonnet 3.5", // legacy version-first order
		"claude-3-5-sonnet-20241022": "Sonnet 3.5",
		"claude-opus-4-8[1m]":        "Opus 4.8 [1m]", // variant preserved
		"claude-code":                "claude-code",   // unknown family passthrough
		"gpt-4o":                     "gpt-4o",        // no claude- prefix
		"":                           "",
	}
	for in, want := range cases {
		if got := modelDisplayName(in); got != want {
			t.Errorf("modelDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelColorHex(t *testing.T) {
	cases := map[string]string{
		"claude-opus-4-8":  "#d3869b",
		"claude-sonnet-5":  "#83a598",
		"claude-haiku-4-5": "#b8bb26",
		"claude-fable-5":   "#fe8019",
		"claude-code":      "",
		"gpt-4o":           "",
	}
	for in, want := range cases {
		if got := modelColorHex(in); got != want {
			t.Errorf("modelColorHex(%q) = %q, want %q", in, got, want)
		}
	}
}
