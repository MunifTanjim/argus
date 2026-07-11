package bundle

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRedactTree(t *testing.T) {
	src := writeExtractedTree(t, false)
	dst := filepath.Join(t.TempDir(), "out")

	rep, err := RedactTree(src, dst, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts["sk-secret"] != 2 {
		t.Fatalf("want 2, got %d", rep.Counts["sk-secret"])
	}

	// jsonl scrubbed.
	body, err := os.ReadFile(filepath.Join(dst, "root", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("sk-secret")) {
		t.Fatal("secret survives in redacted jsonl")
	}
	if !bytes.Contains(body, []byte(RedactPlaceholder)) {
		t.Fatal("placeholder missing from redacted jsonl")
	}

	// manifest metadata scrubbed.
	m, err := ReadManifest(dst)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(m.Metadata.Title), []byte("sk-secret")) {
		t.Fatalf("secret survives in metadata title: %q", m.Metadata.Title)
	}
}
