package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeExtractedTree(t *testing.T, secretInBinary bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifestName),
		mustJSON(t, Manifest{FormatVersion: 1, Agent: "claude", Entry: "root/s.jsonl",
			Metadata: Metadata{Title: "leak sk-secret", FirstMessage: "hi"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "s.jsonl"),
		[]byte(`{"result":"sk-secret in tool output"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if secretInBinary {
		if err := os.WriteFile(filepath.Join(root, "blob.bin"),
			[]byte{0x00, 's', 'k', '-', 's', 'e', 'c', 'r', 'e', 't'}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanCountsAndMetadata(t *testing.T) {
	dir := writeExtractedTree(t, false)
	rep, err := Scan(dir, []string{"sk-secret", "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	// jsonl (1) + metadata Title (1) = 2.
	if rep.Counts["sk-secret"] != 2 {
		t.Fatalf("want 2, got %d", rep.Counts["sk-secret"])
	}
	if zm := rep.ZeroMatch([]string{"sk-secret", "ghost"}); len(zm) != 1 || zm[0] != "ghost" {
		t.Fatalf("want [ghost] zero-match, got %v", zm)
	}
}

func TestScanWarnsBinarySecret(t *testing.T) {
	dir := writeExtractedTree(t, true)
	rep, err := Scan(dir, []string{"sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", rep.Warnings)
	}
}
