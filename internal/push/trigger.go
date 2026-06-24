package push

import (
	"context"
	"log/slog"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Watch consumes a registry/aggregator event stream and dispatches a notification
// each time a session transitions into StatusAwaitingInput — the moment it starts
// blocking on the user (permission prompt, question, plan, or finished turn). It
// fires only on the transition, not on every update, so a waiting session is
// announced once.
//
// Replay events (snapshots the aggregator emits when a source connects or
// reconnects) only record the session's status; they never notify. This is what
// keeps a gateway restart or node reconnect from re-announcing every already-
// waiting session: the status map is rebuilt from the replay without firing, so
// only genuine live transitions afterward notify.
//
// Watch runs until ctx is cancelled or the stream closes; callers pass the
// channel/cancel from Aggregator.Subscribe so the push package stays decoupled
// from the gateway (no import cycle).
func Watch(ctx context.Context, events <-chan registry.Event, d *Dispatcher, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	prev := make(map[string]session.Status)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			s := ev.Session
			if ev.Type == registry.EventRemoved {
				delete(prev, s.ID)
				continue
			}
			if ev.Replay {
				// A snapshot re-stating existing state: record it so the live
				// transition that put the session here isn't replayed as new.
				prev[s.ID] = s.Status
				continue
			}
			was := prev[s.ID]
			prev[s.ID] = s.Status
			if s.Status == session.StatusAwaitingInput && was != session.StatusAwaitingInput {
				log.Info("push: session awaiting input, notifying",
					"session", s.ID, "from", string(was), "type", string(ev.Type), "repo", s.Repo)
				d.Send(ctx, notificationFor(s))
			}
		}
	}
}

// notificationFor renders a session's pending interaction into a user-facing
// notification, with session_id (and node_id) in Data for deep-linking on tap.
func notificationFor(s session.Session) Notification {
	title := s.Repo
	if title == "" {
		title = s.Name
	}
	if title == "" {
		title = "argus session"
	}
	if s.NodeLabel != "" {
		title = s.NodeLabel + " · " + title
	}

	body := "Needs your attention"
	if ix := s.Interaction; ix != nil {
		switch ix.Kind {
		case session.InteractionPermission:
			body = "Permission request"
			if ix.ToolName != "" {
				body = "Permission: " + ix.ToolName
			}
		case session.InteractionQuestion:
			body = "Question"
			if len(ix.Questions) > 0 && ix.Questions[0].Header != "" {
				body = "Question: " + ix.Questions[0].Header
			}
		case session.InteractionPlan:
			body = "Plan ready to review"
		case session.InteractionIdle:
			body = "Finished — waiting for your next message"
			if ix.Message != "" {
				body = ix.Message
			}
		default:
			if ix.Message != "" {
				body = ix.Message
			}
		}
	}

	data := map[string]string{"session_id": s.ID}
	if s.NodeID != "" {
		data["node_id"] = s.NodeID
	}
	return Notification{Title: title, Body: body, Data: data}
}
