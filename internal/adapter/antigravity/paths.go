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
	dir, err := homeDir()
	if err != nil {
		return ""
	}
	return transcriptPathForIn(dir, convID)
}

// transcriptPathForIn resolves a conversation's transcript under an explicit home
// (e.g. an extracted bundle root), instead of the live ~/.gemini/antigravity-cli.
func transcriptPathForIn(home, convID string) string {
	if home == "" || !safeConvID(convID) {
		return ""
	}
	return filepath.Join(home, "brain", convID, ".system_generated", "logs", "transcript_full.jsonl")
}

// conversationDBPath returns a conversation's sqlite db path. Empty when convID is blank/unsafe.
func conversationDBPath(convID string) string {
	dir, err := homeDir()
	if err != nil {
		return ""
	}
	return conversationDBPathIn(dir, convID)
}

func conversationDBPathIn(home, convID string) string {
	if home == "" || !safeConvID(convID) {
		return ""
	}
	return filepath.Join(home, "conversations", convID+".db")
}

// homeFromBrainPath derives the antigravity home from a transcript path laid out
// as <home>/brain/<convID>/...; empty when the path has no brain segment.
func homeFromBrainPath(p string) string {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i, seg := range parts {
		if seg == "brain" {
			return filepath.FromSlash(strings.Join(parts[:i], "/"))
		}
	}
	return ""
}

// resolveHome picks the antigravity home for resolution: derived from rootPath
// (an extracted bundle) when possible, else the live home.
func resolveHome(rootPath string) string {
	if h := homeFromBrainPath(rootPath); h != "" {
		return h
	}
	if dir, err := homeDir(); err == nil {
		return dir
	}
	return ""
}

func safeConvID(convID string) bool {
	return convID != "" && !strings.ContainsAny(convID, `/\`) && !strings.Contains(convID, "..")
}
