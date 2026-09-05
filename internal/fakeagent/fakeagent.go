// Package fakeagent writes the on-disk artifacts a Claude Code process leaves
// behind, so a container can host a discoverable session without the real CLI.
// The field names match what internal/adapter/claudecode reads.
package fakeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter/claudecode/parser"
)

type Config struct {
	PID        int
	SessionID  string
	Name       string
	Cwd        string
	Status     string
	Version    string
	Entrypoint string
}

func (c Config) withDefaults() Config {
	if c.Status == "" {
		c.Status = "idle"
	}
	if c.Version == "" {
		c.Version = "0.0.0-fake"
	}
	if c.Entrypoint == "" {
		c.Entrypoint = "cli"
	}
	return c
}

// SessionFilePath returns ~/.claude/sessions/<pid>.json.
func SessionFilePath(pid int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "sessions", strconv.Itoa(pid)+".json"), nil
}

// Write creates the per-process session file and a two-entry transcript.
func Write(c Config) error {
	c = c.withDefaults()
	if err := writeSessionFile(c); err != nil {
		return err
	}
	return writeTranscript(c)
}

// Remove deletes the per-process session file. A missing file is not an error.
func Remove(pid int) error {
	path, err := SessionFilePath(pid)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeSessionFile(c Config) error {
	path, err := SessionFilePath(c.PID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(map[string]any{
		"pid":        c.PID,
		"sessionId":  c.SessionID,
		"cwd":        c.Cwd,
		"name":       c.Name,
		"status":     c.Status,
		"version":    c.Version,
		"entrypoint": c.Entrypoint,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Timestamps are fixed so that a golden never captures a clock.
const fakeTimestamp = "2026-01-01T00:00:00.000Z"

func writeTranscript(c Config) error {
	dir, err := parser.ProjectDirForPath(c.Cwd)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries := []map[string]any{
		{
			"type":      "user",
			"uuid":      c.SessionID + "-1",
			"timestamp": fakeTimestamp,
			"cwd":       c.Cwd,
			"message":   map[string]any{"role": "user", "content": "hello from " + c.Name},
		},
		{
			"type":      "assistant",
			"uuid":      c.SessionID + "-2",
			"timestamp": fakeTimestamp,
			"cwd":       c.Cwd,
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "text", "text": "hello back from " + c.Name}},
				"model":   "fake-model",
			},
		},
	}
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(dir, c.SessionID+".jsonl"), []byte(b.String()), 0o600)
}
