package mcphttp

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateTokenStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.token")
	first, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if b, err := hex.DecodeString(first); err != nil || len(b) != 32 {
		t.Fatalf("token %q is not 32 hex-encoded bytes", first)
	}
	second, err := LoadOrCreateToken(path)
	if err != nil || second != first {
		t.Fatalf("second: token=%q err=%v", second, err)
	}
}

func TestLoadOrCreateTokenMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.token")
	if _, err := LoadOrCreateToken(path); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestLoadOrCreateTokenRejectsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.token")
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := LoadOrCreateToken(path)
	if err != nil || len(token) != 64 {
		t.Fatalf("token=%q err=%v", token, err)
	}
}

func TestLoadOrCreateTokenTightensMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.token")
	want := strings.Repeat("ab", 32)
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOrCreateToken(path)
	if err != nil || got != want {
		t.Fatalf("token=%q err=%v", got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestLoadOrCreateTokenCreatesParentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if _, err := LoadOrCreateToken(filepath.Join(dir, "mcp.token")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir: %v %v", fi, err)
	}
}

func TestLoadOrCreateTokenRegeneratesInvalid(t *testing.T) {
	for _, content := range []string{"abc", strings.Repeat("AB", 32), strings.Repeat("zz", 32)} {
		path := filepath.Join(t.TempDir(), "mcp.token")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadOrCreateToken(path)
		if err != nil || got == content || len(got) != 64 {
			t.Fatalf("%q: token=%q err=%v", content, got, err)
		}
	}
}

func TestLoadOrCreateTokenAcceptsTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.token")
	want := strings.Repeat("0f", 32)
	if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOrCreateToken(path)
	if err != nil || got != want {
		t.Fatalf("token=%q err=%v", got, err)
	}
}
