package jsonc

import (
	"strings"
	"testing"
)

var nested = fuzzEntrySets[1]

func TestSetUnsetNestedRoundTrip(t *testing.T) {
	for _, src := range []string{
		"{\n  \"model\": \"m\"\n}\n",
		`{"model":"m"}`,
		`{"permissions":{}}`,
		`{"permissions":{"allow":[]},"mcpServers":{}}`,
		"{\n  // c\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls *)\", // l\n    ],\n  },\n}\n",
	} {
		installed, err := Set([]byte(src), nested...)
		if err != nil {
			t.Fatalf("Set(%q): %v", src, err)
		}
		if again, err := Set(installed, nested...); err != nil || string(again) != string(installed) {
			t.Fatalf("second Set(%q) = %q, %v; want no change", src, again, err)
		}
		back, err := Unset(installed, false, nested...)
		if err != nil || string(back) != src {
			t.Fatalf("Unset(Set(%q)) = %q, %v\ninstalled: %q", src, back, err, installed)
		}
	}
}

func TestUnsetDropsCreatedRoot(t *testing.T) {
	installed, err := Set(nil, nested[0])
	if err != nil {
		t.Fatal(err)
	}
	if back, err := Unset(installed, true, nested[0]); err != nil || len(back) != 0 {
		t.Fatalf("Unset with dropRoot = %q, %v; want empty", back, err)
	}
	if back, err := Unset(installed, false, nested[0]); err != nil || string(back) != "{\n}\n" {
		t.Fatalf("Unset without dropRoot = %q, %v", back, err)
	}
	if back, err := Unset([]byte("{}"), true, nested[0]); err != nil || string(back) != "{}" {
		t.Fatalf("Unset of a user root = %q, %v", back, err)
	}
}

func TestSetRefusesWrongKind(t *testing.T) {
	for _, src := range []string{`{"permissions":[]}`, `{"permissions":{"allow":{}}}`, `{"mcpServers":[]}`, `[]`} {
		if out, err := Set([]byte(src), nested...); err == nil {
			t.Fatalf("Set(%q) = %q; want an error", src, out)
		}
	}
	if _, err := Set([]byte(`{"permissions":{"allow":"x"}}`), nested[1]); err == nil || !strings.Contains(err.Error(), "permissions.allow is not an array") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnsetKeepsCommentsBesideSameLineItem(t *testing.T) {
	for src, want := range map[string]string{
		`{"mcpServers": {"other": {}, /* keep */ "argus": {"serverUrl": "u"}}}`:             `{"mcpServers": {"other": {} /* keep */}}`,
		`{"mcpServers": {"other": {} /* keep */, "argus": {"serverUrl": "u"}}}`:             `{"mcpServers": {"other": {} /* keep */}}`,
		`{"mcpServers": {"other": {}, /* keep */ "argus": {"serverUrl": "u"}, "last": {}}}`: `{"mcpServers": {"other": {} /* keep */, "last": {}}}`,
		`{"permissions": {"allow": ["Bash", /* keep */ "mcp(argus/*)"]}}`:                   `{"permissions": {"allow": ["Bash" /* keep */]}}`,
		`{"mcpServers": {"other": {}, "argus": {"serverUrl": "u"}}}`:                        `{"mcpServers": {"other": {}}}`,
	} {
		got, err := Unset([]byte(src), false, nested...)
		if err != nil || string(got) != want {
			t.Errorf("Unset(%s) = %s, %v; want %s", src, got, err, want)
		}
	}
}
