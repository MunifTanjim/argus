package antigravity

import (
	"os"
	"path/filepath"
	"strings"
)

// homeDirOverride redirects the Antigravity CLI home in tests.
var homeDirOverride string

func homeDir() (string, error) {
	if homeDirOverride != "" {
		return homeDirOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", "antigravity-cli"), nil
}

func geminiDir() (string, error) {
	dir, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Dir(dir), nil
}

func hooksJSONPath() (string, error) { return sub("hooks.json") }
func brainDir() (string, error)      { return sub("brain") }

// configHooksJSONPath returns the second hooks file agy reads (~/.gemini/config/hooks.json).
func configHooksJSONPath() (string, error) {
	dir, err := geminiDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config", "hooks.json"), nil
}

func sub(name string) (string, error) {
	dir, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// transcriptPathFor returns a conversation's transcript_full.jsonl path. Empty when
// convID is blank/unsafe. The file need not exist.
func transcriptPathFor(convID string) string {
	dir, err := brainDir()
	if err != nil || !safeConvID(convID) {
		return ""
	}
	return filepath.Join(dir, convID, ".system_generated", "logs", "transcript_full.jsonl")
}

// conversationDBPath returns a conversation's sqlite db path. Empty when convID is blank/unsafe.
func conversationDBPath(convID string) string {
	dir, err := homeDir()
	if err != nil || !safeConvID(convID) {
		return ""
	}
	return filepath.Join(dir, "conversations", convID+".db")
}

func safeConvID(convID string) bool {
	return convID != "" && !strings.ContainsAny(convID, `/\`) && !strings.Contains(convID, "..")
}
