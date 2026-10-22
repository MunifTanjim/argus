package jsonc

import (
	"bytes"
	"testing"
)

type fuzzServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Enabled bool              `json:"enabled"`
}

// fuzzEntrySets are the shapes argus owns: a server member and a rule element in
// a root array (OpenCode), and a server member and a string element two levels
// down (Antigravity, Claude).
var fuzzEntrySets = [][]Entry{
	{
		{Path: []string{"mcp"}, Key: "argus", Value: fuzzServer{Type: "remote", URL: "http://127.0.0.1:1/mcp/opencode", Headers: map[string]string{"Authorization": "Bearer t"}, Enabled: true}},
		{Path: []string{"permissions"}, Value: map[string]string{"action": "argus_*", "resource": "*", "effect": "allow"}},
	},
	{
		{Path: []string{"mcpServers"}, Key: "argus", Value: map[string]any{"disabled": false, "serverUrl": "http://127.0.0.1:1/mcp/antigravity"}},
		{Path: []string{"permissions", "allow"}, Value: "mcp(argus/*)"},
	},
}

func FuzzRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"model\": \"m\",\n  \"mcp\": {\n    \"other\": {\"type\": \"local\"}\n  },\n  \"permissions\": [\n    {\"action\": \"bash\", \"resource\": \"*\", \"effect\": \"ask\"}\n  ]\n}\n",
		"{\n  // c\n  \"plugins\": [\"p\"], /* b */\n  \"mcp\": {\n    \"other\": {}, // o\n  },\n  \"model\": \"m\", // t\n}\n",
		`{"model":"m","mcp":{"other":{}},"permissions":[]}`,
		`{"model":"m"}`,
		"{\n  \"model\": \"m\",\n  \"mcp\": {}\n}\n",
		"{\r\n  \"model\": \"m\",\r\n  \"permissions\": []\r\n}\r\n",
		"{\n  \"model\": \"m\"\n}",
		"{}",
		"{\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls *)\"\n    ],\n    \"deny\": []\n  },\n  \"mcpServers\": {}\n}\n",
		`{"permissions":{},"mcpServers":{"x":{"serverUrl":"u"}}}`,
		`{"permissions":{"allow":[]}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(bytes.TrimSpace(src)) == 0 {
			return // argus creates the file
		}
		cfg, err := Decode(src)
		if err != nil {
			return
		}
		for _, entries := range fuzzEntrySets {
			if owned(cfg, entries) {
				continue
			}
			installed, err := Set(src, entries...)
			if err != nil {
				continue
			}
			for _, dropRoot := range []bool{false, true} {
				back, err := Unset(installed, dropRoot, entries...)
				if err != nil {
					continue
				}
				if !bytes.Equal(back, src) {
					t.Fatalf("round trip changed the config (dropRoot %v)\nsrc:  %q\ninst: %q\nback: %q", dropRoot, src, installed, back)
				}
			}
		}
	})
}

// owned reports whether cfg already holds any of entries.
func owned(cfg map[string]any, entries []Entry) bool {
	for _, e := range entries {
		if _, found := strip(deepCopy(cfg), e); found {
			return true
		}
	}
	return false
}

func deepCopy(v map[string]any) map[string]any {
	return toAny(v).(map[string]any)
}
