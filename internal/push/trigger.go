package push

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Watch dispatches a notification each time a session transitions into
// StatusAwaitingInput. It fires only on the transition, so a waiting session is
// announced once.
//
// Replay events (snapshots emitted on source connect/reconnect) only record
// status, never notify — this keeps a gateway restart or node reconnect from
// re-announcing every already-waiting session.
//
// Runs until ctx is cancelled or the stream closes. Takes the channel as a param
// (rather than importing the aggregator) to avoid an import cycle.
func Watch(ctx context.Context, events <-chan registry.Event, sinks []Sink, log *slog.Logger) {
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
				// Record without firing so the original live transition isn't replayed as new.
				prev[s.ID] = s.Status
				continue
			}
			was := prev[s.ID]
			prev[s.ID] = s.Status
			if s.Status == session.StatusAwaitingInput && was != session.StatusAwaitingInput {
				log.Info("push: session awaiting input, notifying",
					"session", s.ID, "from", string(was), "type", string(ev.Type), "repo", s.Repo)
				n := notificationFor(s)
				for _, sink := range sinks {
					sink.Notify(ctx, n)
				}
			}
		}
	}
}

// notificationFor renders a session's pending interaction into a notification,
// with session_id (and node_id) in Data for deep-linking on tap.
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
			body = "Permission Request"
			if ix.ToolName != "" {
				body = "Permission: " + ix.ToolName
			}
		case session.InteractionQuestion:
			body = "Question"
			if len(ix.Questions) > 1 {
				body = strconv.Itoa(len(ix.Questions)) + " Questions"
			} else if len(ix.Questions) == 1 && ix.Questions[0].Header != "" {
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
