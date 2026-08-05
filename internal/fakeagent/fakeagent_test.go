package fakeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesSessionFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Config{PID: 4242, SessionID: "sid-1", Name: "alpha", Cwd: home}
	if err := Write(cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", "4242.json"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["sessionId"] != "sid-1" || got["name"] != "alpha" {
		t.Fatalf("session file = %v, want sessionId sid-1 and name alpha", got)
	}
	if got["entrypoint"] != "cli" {
		t.Fatalf("entrypoint = %v, want cli", got["entrypoint"])
	}
}

func TestWriteCreatesTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Config{PID: 7, SessionID: "sid-2", Name: "beta", Cwd: home}
	if err := Write(cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", "sid-2.jsonl"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("transcript matches = %v, want exactly one", matches)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(firstLine(string(b))), &line); err != nil {
		t.Fatalf("first line is not JSON: %v", err)
	}
	if line["type"] != "user" {
		t.Fatalf("first entry type = %v, want user", line["type"])
	}
}

func TestRemoveDeletesSessionFileAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := Write(Config{PID: 9, SessionID: "sid-3", Cwd: home}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Remove(9); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "sessions", "9.json")); !os.IsNotExist(err) {
		t.Fatalf("session file still present, stat err = %v", err)
	}
	if err := Remove(9); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func firstLine(s string) string {
	for i := range s {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
