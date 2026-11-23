package memory

import (
	"strings"
	"testing"
)

func TestLooksSecret(t *testing.T) {
	pos := map[string]string{
		"aws":        "key AKIAABCDEFGHIJKLMNOP here",
		"github":     "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat": "github_pat_abcdefghijklmnopqrstuv",
		"slack":      "xoxb-1234567890-abcdef",
		"openai":     "sk-abcdefghijklmnopqrstuvwx",
		"anthropic":  "sk-ant-abcdefghijklmnopqrstuvwx",
		"google":     "AIza" + "abcdefghijklmnopqrstuvwxyz0123456789",
		"private":    "-----BEGIN RSA PRIVATE KEY-----",
		"jwt":        "eyJhbGciOi.eyJzdWIiOi.abc_def-ghi",
		"generic":    "password: hunter2hunter2",
		"generic2":   "API_KEY=abcdefgh1234",
		"card":       "card 4111 1111 1111 1111",
		"card2":      "4111-1111-1111-1111",
	}
	for name, s := range pos {
		if rule, ok := LooksSecret(s); !ok || rule == "" {
			t.Errorf("%s: expected match for %q", name, s)
		}
	}
	neg := []string{
		"prefers tabs over spaces",
		"AKIA short",
		"sk-short",
		"password: short",
		"the token is stored elsewhere",
		"order 4111 1111 1111 1112",
		"phone 1234567890",
		"-----BEGIN CERTIFICATE-----",
		"task-tracking-dashboard-v2",
		"risk-assessment-framework",
		"MAKIAABCDEFGHIJKLMNOP",
	}
	for _, s := range neg {
		if rule, ok := LooksSecret(s); ok {
			t.Errorf("unexpected %s match for %q", rule, s)
		}
	}
}

func TestLuhn(t *testing.T) {
	if !luhn("4111111111111111") || luhn("4111111111111112") {
		t.Fatal("luhn")
	}
}

func TestRedact(t *testing.T) {
	got := Redact("use abc123 and abc123456 with x", map[string]string{"TOKEN": "abc123", "LONG": "abc123456", "EMPTY": ""})
	if want := "use «TOKEN» and «LONG» with x"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMaskSecrets(t *testing.T) {
	in := "key AKIAABCDEFGHIJKLMNOP and card 4111 1111 1111 1111 ok"
	got := MaskSecrets(in)
	if strings.Contains(got, "AKIA") || strings.Contains(got, "4111") {
		t.Fatalf("not masked: %q", got)
	}
	if got != "key «aws-access-key» and card «credit-card» ok" {
		t.Fatalf("masked = %q", got)
	}
	if got := MaskSecrets("-----BEGIN RSA PRIVATE KEY-----"); got != "«private-key»" {
		t.Fatalf("private key masked = %q", got)
	}
	if MaskSecrets("plain text") != "plain text" {
		t.Fatal("plain text changed")
	}
}
