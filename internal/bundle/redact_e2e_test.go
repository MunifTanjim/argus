package bundle

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteDirRoundTrip(t *testing.T) {
	src := writeExtractedTree(t, false)
	redacted := filepath.Join(t.TempDir(), "red")
	if _, err := RedactTree(src, redacted, []string{"sk-secret"}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := WriteDir(&buf, redacted); err != nil {
		t.Fatal(err)
	}

	// Re-read the bundle and confirm the secret is gone and it still parses.
	out := t.TempDir()
	m, err := Read(&buf, out)
	if err != nil {
		t.Fatal(err)
	}
	if m.Entry != "root/s.jsonl" {
		t.Fatalf("entry changed: %q", m.Entry)
	}
	body, err := os.ReadFile(filepath.Join(out, "root", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("sk-secret")) {
		t.Fatal("secret survives a full redact->rebundle->read cycle")
	}
}
