//go:build opencode_live

package opencode

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLiveSmoke(t *testing.T) {
	info, ok := readServiceInfo()
	if !ok {
		t.Skip("opencode service not running")
	}

	c := newClient(info)
	ctx := context.Background()

	t.Run("sessions", func(t *testing.T) {
		sessions, err := c.listSessions(ctx)
		if err != nil {
			t.Fatalf("listSessions: %v", err)
		}
		for _, s := range sessions {
			if s.Location.Directory == "" {
				t.Errorf("session %s has empty Location.Directory", s.ID)
			}
		}
		t.Logf("sessions: %d", len(sessions))

		if len(sessions) > 0 {
			msgs, err := c.readMessages(ctx, sessions[0].ID)
			if err != nil {
				t.Fatalf("readMessages: %v", err)
			}
			_ = foldMessages(msgs)
			t.Logf("messages in first session: %d", len(msgs))
		}
	})

	t.Run("event_stream", func(t *testing.T) {
		tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		body, err := c.openEvents(tctx)
		if err != nil {
			t.Fatalf("openEvents: %v", err)
		}
		defer body.Close()

		var frames []sseFrame
		_ = parseSSE(body, func(f sseFrame) {
			frames = append(frames, f)
		})

		if len(frames) == 0 {
			t.Fatal("no SSE frames received")
		}
		if frames[0].Type != "server.connected" {
			t.Errorf("first frame type = %q, want server.connected", frames[0].Type)
		}
		types := make([]string, 0, len(frames))
		for _, f := range frames {
			types = append(types, f.Type)
		}
		t.Logf("received frames: %s", strings.Join(types, ", "))
	})
}
