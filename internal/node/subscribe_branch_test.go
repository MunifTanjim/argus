package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// TestSubscribeRefreshesBranch proves opening a session (transcript.subscribe)
// recomputes the git branch from its cwd and publishes it, so a mid-session
// checkout is picked up when the session is next opened.
func TestSubscribeRefreshesBranch(t *testing.T) {
	d := newNode(nil)

	// A git repo dir with a branch, used as the session cwd.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/feat/session-git-branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tmp := writeTempTranscript(t)
	s, _ := d.reg.ApplyHook(registry.HookUpdate{
		AgentSessionID: "b1",
		Cwd:            repo,
		TranscriptPath: tmp,
		Status:         session.StatusIdle,
	})

	// Subscribe to registry events after the session exists so the channel is clean.
	events, cancel := d.reg.Subscribe()
	defer cancel()

	fn := &fakeNotifier{ch: make(chan api.Notification, 8)}
	ctx := api.WithNotifier(context.Background(), fn)
	if _, err := d.handleTranscriptSubscribe(ctx, mustJSON(api.TranscriptSubscribeParams{
		SubID:     "sub",
		SessionID: s.ID,
	})); err != nil {
		t.Fatalf("handleTranscriptSubscribe: %v", err)
	}

	if got, _ := d.reg.Get(s.ID); got.Branch != "feat/session-git-branch" {
		t.Fatalf("stored branch = %q, want feat/session-git-branch", got.Branch)
	}

	select {
	case ev := <-events:
		if ev.Type != registry.EventUpdated || ev.Session.Branch != "feat/session-git-branch" {
			t.Fatalf("event = %+v, want updated with branch feat/session-git-branch", ev)
		}
	default:
		t.Fatal("expected an EventUpdated carrying the branch")
	}
}
