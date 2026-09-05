package node

import (
	"strings"
	"testing"
	"time"
)

func wsURL(u string) string { return "ws" + strings.TrimPrefix(u, "http") }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for condition")
}
