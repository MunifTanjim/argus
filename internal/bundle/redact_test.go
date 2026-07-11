package bundle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRedactTextBytes(t *testing.T) {
	in := []byte(`{"result":"key sk-abc123 and again sk-abc123"}`)
	out, counts := redactTextBytes(in, []string{"sk-abc123", "nope"})
	if string(out) != `{"result":"key [REDACTED] and again [REDACTED]"}` {
		t.Fatalf("unexpected output: %s", out)
	}
	if counts["sk-abc123"] != 2 {
		t.Fatalf("want 2 occurrences, got %d", counts["sk-abc123"])
	}
	if counts["nope"] != 0 {
		t.Fatalf("want 0 for absent literal, got %d", counts["nope"])
	}
}

func TestRedactJSONEscapedQuote(t *testing.T) {
	// A secret with a quote is stored backslash-escaped inside a JSONL line, so
	// the raw literal the user pastes never appears verbatim on disk.
	secret := `p"ass`
	line, err := json.Marshal(map[string]string{"token": secret})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(line, []byte(secret)) {
		t.Fatalf("precondition: raw secret should not appear verbatim in %s", line)
	}
	out, counts := redactTextBytes(line, []string{secret})
	if string(out) != `{"token":"[REDACTED]"}` {
		t.Fatalf("escaped secret not redacted: %s", out)
	}
	if counts[secret] != 1 {
		t.Fatalf("want escaped occurrence counted against the literal, got %d", counts[secret])
	}
}

func TestRedactJSONEscapedHTML(t *testing.T) {
	// encoding/json escapes < > & by default (< etc.); redaction must match
	// that on-disk form too.
	secret := "a<b>c"
	line, err := json.Marshal(map[string]string{"v": secret})
	if err != nil {
		t.Fatal(err)
	}
	out, counts := redactTextBytes(line, []string{secret})
	if bytes.Contains(out, []byte("u003c")) || counts[secret] == 0 {
		t.Fatalf("HTML-escaped secret survived: out=%s counts=%d", out, counts[secret])
	}
}

func TestRedactTextBytesEmptyLiteralIgnored(t *testing.T) {
	in := []byte("hello")
	out, _ := redactTextBytes(in, []string{""})
	if string(out) != "hello" {
		t.Fatalf("empty literal must not alter input, got %s", out)
	}
}

func TestClassifyFile(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(text, []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sq := filepath.Join(dir, "c.db")
	if err := os.WriteFile(sq, append([]byte("SQLite format 3\x00"), 0, 0, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(bin, []byte{0xff, 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want fileKind
	}{{text, kindText}, {sq, kindSQLite}, {bin, kindOtherBinary}} {
		got, err := classifyFile(tc.path)
		if err != nil {
			t.Fatalf("classify %s: %v", tc.path, err)
		}
		if got != tc.want {
			t.Fatalf("classify %s: want %v got %v", tc.path, tc.want, got)
		}
	}
}
