package bot

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

// Hand-off states.
const (
	HandoffPending  = "pending"
	HandoffAnswered = "answered"
	HandoffNoAnswer = "no_answer"
)

// Handoff is one bot asking another for work. A local hand-off is one row
// on the node; a cross-node one has a row on each side: FromNode is set on
// the answering node's row, ToNode on the asking node's.
type Handoff struct {
	ID, RootID string
	Depth      int
	// Ancestors are the bots up the chain, the asker last, as "node:bot".
	Ancestors                               []string
	FromNode, FromBot, FromName, FromThread string
	FromSession                             string
	ToNode, ToBot, ToName, ToThread         string
	Request                                 json.RawMessage
	State, Answer                           string
	CreatedAt, AnsweredAt                   time.Time
}

func handoffFrom(r gen.BotHandoff) Handoff {
	h := Handoff{
		ID: r.ID, RootID: r.RootID, Depth: int(r.Depth), FromNode: r.FromNode, FromBot: r.FromBot,
		FromName: r.FromName, FromThread: r.FromThread, FromSession: r.FromSession, ToNode: r.ToNode,
		ToBot: r.ToBot, ToName: r.ToName, ToThread: r.ToThread, Request: json.RawMessage(r.Request),
		State: r.State, Answer: r.Answer, CreatedAt: time.Unix(0, r.CreatedAt), AnsweredAt: timeOrZero(r.AnsweredAt),
	}
	_ = json.Unmarshal([]byte(r.Ancestors), &h.Ancestors)
	return h
}

func timeOrZero(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// InsertHandoff records a pending hand-off; CreatedAt defaults to now.
func (s *Store) InsertHandoff(ctx context.Context, h Handoff) error {
	anc, err := json.Marshal(h.Ancestors)
	if err != nil {
		return err
	}
	req := string(h.Request)
	if req == "" {
		req = "{}"
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.now()
	}
	return s.q.InsertBotHandoff(ctx, gen.InsertBotHandoffParams{
		ID: h.ID, RootID: h.RootID, Depth: int64(h.Depth), Ancestors: string(anc), FromNode: h.FromNode,
		FromBot: h.FromBot, FromName: h.FromName, FromThread: h.FromThread, FromSession: h.FromSession,
		ToNode: h.ToNode, ToBot: h.ToBot, ToName: h.ToName, ToThread: h.ToThread, Request: req,
		State: HandoffPending, CreatedAt: h.CreatedAt.UnixNano(),
	})
}

func (s *Store) Handoff(ctx context.Context, id string) (Handoff, bool, error) {
	r, err := s.q.GetBotHandoff(ctx, id)
	return one(r, err, handoffFrom)
}

// HandoffByThread returns the hand-off a run thread answers.
func (s *Store) HandoffByThread(ctx context.Context, threadID string) (Handoff, bool, error) {
	r, err := s.q.BotHandoffByToThread(ctx, threadID)
	return one(r, err, handoffFrom)
}

// HandoffsInRoot counts the hand-offs of a chain recorded on this node.
func (s *Store) HandoffsInRoot(ctx context.Context, rootID string) (int, error) {
	n, err := s.q.CountBotHandoffsByRoot(ctx, rootID)
	return int(n), err
}

func (s *Store) SetHandoffThread(ctx context.Context, id, threadID string) error {
	return s.q.SetBotHandoffToThread(ctx, gen.SetBotHandoffToThreadParams{ToThread: threadID, ID: id})
}

// EndHandoff moves a pending hand-off to state with its answer or reason. ok
// is false when it was no longer pending.
func (s *Store) EndHandoff(ctx context.Context, id, state, answer string) (bool, error) {
	n, err := s.q.EndBotHandoff(ctx, gen.EndBotHandoffParams{State: state, Answer: answer, AnsweredAt: s.now().UnixNano(), ID: id})
	return n > 0, err
}

// PendingHandoffsBefore lists this node's own pending asks created before t.
func (s *Store) PendingHandoffsBefore(ctx context.Context, t time.Time) ([]Handoff, error) {
	rows, err := s.q.PendingBotHandoffsBefore(ctx, t.UnixNano())
	if err != nil {
		return nil, err
	}
	out := make([]Handoff, len(rows))
	for i, r := range rows {
		out[i] = handoffFrom(r)
	}
	return out, nil
}
