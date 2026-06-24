package tmux

import (
	"strings"
	"time"
)

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

func endsWith(s, suffix string) bool { return strings.HasSuffix(s, suffix) }

func baseOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// waitFor polls cond up to ~2s, returning true as soon as it passes.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return cond()
}
