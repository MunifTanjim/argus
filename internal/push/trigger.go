package push

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/MunifTanjim/argus/internal/registry"
	"github.com/MunifTanjim/argus/internal/session"
)

// Sinks groups a Watch's delivery targets. Immediate sinks fire on the
// awaiting-input transition. Delayed sinks fire after Delay, re-checked when it
// elapses; Delay 0 makes them fire on the transition like Immediate.
type Sinks struct {
	Immediate []Sink
	Delayed   []Sink
	Delay     time.Duration
}

// Watch fires on each transition into StatusAwaitingInput (once per transition).
// Replay events only record status, never notify, so a restart doesn't
// re-announce every already-waiting session.
func Watch(ctx context.Context, events <-chan registry.Event, sinks Sinks, log *slog.Logger) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	prev := make(map[string]session.Status)

	// armedEntry is a scheduled (delayed) mobile push awaiting its grace period.
	// sess is refreshed as events arrive so the fire-time render is current.
	type armedEntry struct {
		id    string
		timer *time.Timer
		sess  session.Session
	}
	armed := make(map[string]*armedEntry)
	fired := make(chan *armedEntry, 1) // a timer signals its entry back into the loop

	// loopDone releases any timer callback still blocked on `fired` once Watch
	// returns — needed for the events-closed exit, where ctx stays live and so
	// ctx.Done() alone would strand the callback.
	loopDone := make(chan struct{})
	defer close(loopDone)

	stopAll := func() {
		for _, e := range armed {
			e.timer.Stop()
		}
	}
	inAttention := func(st session.Status) bool {
		return st == session.StatusAwaitingInput || st == session.StatusIdle
	}

	for {
		select {
		case <-ctx.Done():
			stopAll()
			return
		case e := <-fired:
			// A same-instant EventRemoved is not guaranteed to win this select
			// tie, so a delayed push may still fire for a session removed at the
			// boundary. Acceptable: the grace genuinely elapsed while it was
			// awaiting. Removal processed first suppresses via the guard below.
			if armed[e.id] != e {
				continue // cancelled or re-armed since this timer was set
			}
			delete(armed, e.id)
			if !inAttention(prev[e.id]) {
				continue // no longer needs attention (e.g. answered at the boundary)
			}
			n := notificationFor(e.sess)
			for _, sink := range sinks.Delayed {
				sink.Notify(ctx, n)
			}
		case ev, ok := <-events:
			if !ok {
				stopAll()
				return
			}
			s := ev.Session
			if ev.Type == registry.EventRemoved {
				delete(prev, s.ID)
				if e := armed[s.ID]; e != nil {
					e.timer.Stop()
					delete(armed, s.ID)
				}
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
				for _, sink := range sinks.Immediate {
					sink.Notify(ctx, n)
				}
				if len(sinks.Delayed) > 0 {
					if sinks.Delay <= 0 {
						for _, sink := range sinks.Delayed {
							sink.Notify(ctx, n)
						}
					} else {
						if e := armed[s.ID]; e != nil {
							e.timer.Stop()
						}
						e := &armedEntry{id: s.ID, sess: s}
						e.timer = time.AfterFunc(sinks.Delay, func() {
							select {
							case fired <- e:
							case <-ctx.Done():
							case <-loopDone:
							}
						})
						armed[s.ID] = e
					}
				}
			} else if e := armed[s.ID]; e != nil {
				// Not an awaiting transition: keep the armed snapshot fresh, or
				// cancel if the session left the attention set (e.g. answered →
				// working) before the grace elapses.
				if inAttention(s.Status) {
					e.sess = s
				} else {
					e.timer.Stop()
					delete(armed, s.ID)
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
