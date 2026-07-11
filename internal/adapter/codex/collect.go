package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// collectSessionFiles gathers the main rollout and every transitively spawned
// subagent rollout, rooted under ~/.codex so subagents resolve offline.
func collectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	abs, err := filepath.Abs(transcriptPath)
	if err != nil {
		return nil, err
	}
	home, err := codexHome()
	if err != nil {
		return nil, err
	}
	home, _ = filepath.Abs(home)

	// The entry must be a real rollout under <home>/sessions, not any other readable
	// file; everything else is discovered from it.
	if !adapter.WithinDir(filepath.Join(home, "sessions"), abs) || !strings.HasSuffix(abs, ".jsonl") {
		return nil, fmt.Errorf("collect: %s is not a session rollout", abs)
	}
	if info, err := os.Lstat(abs); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("collect: %s is not a regular file", abs)
	}
	sessionsRoot := sessionsRootFrom(abs)

	var out []adapter.BundledFile
	seen := make(map[string]bool)
	var walk func(path string)
	walk = func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		// Lstat guards against a symlinked rollout escaping the home scope.
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			return
		}
		if bf, ok := adapter.RootedFile(home, path); ok {
			out = append(out, bf)
		}
		chunks, err := parseRollout(path)
		if err != nil {
			return
		}
		for _, c := range chunks {
			for _, it := range c.Items {
				for _, sub := range it.Subagents {
					if sub.ID != "" {
						walk(findRolloutPathIn(sessionsRoot, sub.ID))
					}
				}
			}
		}
	}
	walk(abs)
	return out, nil
}
