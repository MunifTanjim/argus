package antigravity

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// collectSessionFiles gathers the main transcript plus every transitively invoked
// subagent's transcript and conversation db, rooted under the antigravity home so
// subagents resolve offline.
func collectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	abs, err := filepath.Abs(transcriptPath)
	if err != nil {
		return nil, err
	}
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	home, _ = filepath.Abs(home)

	// The entry must be a real conversation transcript under <home>/brain, not any
	// other readable file; everything else is derived from it.
	if !adapter.WithinDir(filepath.Join(home, "brain"), abs) || filepath.Base(abs) != "transcript_full.jsonl" {
		return nil, fmt.Errorf("collect: %s is not a session transcript", abs)
	}
	if info, err := os.Lstat(abs); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("collect: %s is not a regular file", abs)
	}

	var out []adapter.BundledFile
	add := func(p string) {
		if p == "" {
			return
		}
		// Lstat, not Stat: skip symlinks whose target could sit outside home.
		if info, err := os.Lstat(p); err != nil || !info.Mode().IsRegular() {
			return
		}
		if bf, ok := adapter.RootedFile(home, p); ok {
			out = append(out, bf)
		}
	}

	seen := make(map[string]bool)
	var walk func(convID, tpath string)
	walk = func(convID, tpath string) {
		if convID == "" || seen[convID] {
			return
		}
		seen[convID] = true
		add(tpath)
		add(conversationDBPath(convID))
		chunks, err := parseTranscript(tpath)
		if err != nil {
			return
		}
		for _, c := range chunks {
			for _, it := range c.Items {
				for _, sub := range it.Subagents {
					if sub.ID != "" {
						walk(sub.ID, transcriptPathFor(sub.ID))
					}
				}
			}
		}
	}
	walk(convIDFromPath(abs), abs)
	return out, nil
}
