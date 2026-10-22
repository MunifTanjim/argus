package claudecode

import (
	"strings"
	"testing"
)

func TestModShipsArgusAgentsSkill(t *testing.T) {
	var body string
	err := walkInstallable(func(rel string, b []byte) error {
		if rel == "skills/argus-agents/SKILL.md" {
			body = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "name: argus-agents") {
		t.Fatalf("skill missing or without frontmatter name: %q", body)
	}
}
