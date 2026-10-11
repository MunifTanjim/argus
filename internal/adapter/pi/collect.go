package pi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MunifTanjim/argus/internal/adapter"
)

// collectSessionFiles bundles the session .jsonl itself. Unlike agents with
// subagent sidecars, a pi session is a single file (extensions persist state
// as custom entries inside it).
func collectSessionFiles(transcriptPath string) ([]adapter.BundledFile, error) {
	abs, err := filepath.Abs(transcriptPath)
	if err != nil {
		return nil, err
	}
	root, err := sessionsRoot()
	if err != nil {
		return nil, err
	}
	root, _ = filepath.Abs(root)
	if !adapter.WithinDir(root, abs) || !strings.HasSuffix(abs, ".jsonl") {
		return nil, fmt.Errorf("collect: %s is not a pi session file", abs)
	}
	if info, err := os.Lstat(abs); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("collect: %s is not a regular file", abs)
	}
	bf, _ := adapter.RootedFile(filepath.Dir(root), abs)
	return []adapter.BundledFile{bf}, nil
}
