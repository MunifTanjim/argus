// Command fakeclaude imitates the on-disk footprint of a Claude Code process.
// The e2elive image installs it as `claude`, because discovery matches the
// argv0 basename. It blocks until it is signalled.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/MunifTanjim/argus/internal/fakeagent"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude: getwd:", err)
		os.Exit(1)
	}
	cfg := fakeagent.Config{
		PID:       os.Getpid(),
		SessionID: env("FAKE_CLAUDE_SESSION_ID", "fake-session"),
		Name:      env("FAKE_CLAUDE_NAME", "fake"),
		Status:    env("FAKE_CLAUDE_STATUS", "idle"),
		Cwd:       cwd,
	}
	if err := fakeagent.Write(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude:", err)
		os.Exit(1)
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	<-ch

	if err := fakeagent.Remove(cfg.PID); err != nil {
		fmt.Fprintln(os.Stderr, "fakeclaude:", err)
		os.Exit(1)
	}
}
