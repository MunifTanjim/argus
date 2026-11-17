package orch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db/gen"
)

// OutboxItem is one call queued for another node.
type OutboxItem struct {
	Seq    int64
	Node   string
	Method string
	Params json.RawMessage
	// Attempts counts the error replies the peer gave this call.
	Attempts int
}

// Enqueue queues method with params for node, after the calls already queued for it.
func (s *Store) Enqueue(ctx context.Context, node, method string, params any) error {
	if err := s.enqueue(ctx, s.q, node, method, params); err != nil {
		return err
	}
	s.signalEnqueued()
	return nil
}

// InsertAndEnqueue stores m as Insert does and queues method for node with the
// params built from the stored m, in one transaction: on error, neither is kept.
func (s *Store) InsertAndEnqueue(ctx context.Context, m api.Message, node, method string, params func(api.Message) any) (api.Message, error) {
	return s.InsertRequestAndEnqueue(ctx, m, Request{}, node, method, params)
}

// InsertRequestAndEnqueue is InsertAndEnqueue for a message sent with a request_id.
func (s *Store) InsertRequestAndEnqueue(ctx context.Context, m api.Message, req Request, node, method string, params func(api.Message) any) (api.Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return api.Message{}, fmt.Errorf("orch: insert and enqueue: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	m, err = s.insert(ctx, q, m, req)
	if err != nil {
		return api.Message{}, err
	}
	if err := s.enqueue(ctx, q, node, method, params(m)); err != nil {
		return api.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return api.Message{}, fmt.Errorf("orch: insert and enqueue: %w", err)
	}
	s.signalEnqueued()
	return m, nil
}

func (s *Store) enqueue(ctx context.Context, q *gen.Queries, node, method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("orch: encode %s: %w", method, err)
	}
	if err := q.InsertOrchOutbox(ctx, gen.InsertOrchOutboxParams{
		Node: node, Method: method, Params: string(b), CreatedAt: s.now().UnixNano(),
	}); err != nil {
		return fmt.Errorf("orch: enqueue: %w", err)
	}
	return nil
}

func (s *Store) signalEnqueued() {
	select {
	case s.enqueued <- struct{}{}:
	default:
	}
}

// Enqueued is signaled after each Enqueue; signals that arrive before the
// previous one was received fold into it.
func (s *Store) Enqueued() <-chan struct{} { return s.enqueued }

// Pending returns up to limit calls queued for node, oldest first.
func (s *Store) Pending(ctx context.Context, node string, limit int) ([]OutboxItem, error) {
	rows, err := s.q.PendingOrchOutbox(ctx, gen.PendingOrchOutboxParams{Node: node, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, OutboxItem{Seq: r.Seq, Node: r.Node, Method: r.Method, Params: json.RawMessage(r.Params), Attempts: int(r.Attempts)})
	}
	return out, nil
}

// Fail counts an error reply from the peer for the call seq, at most once per
// minGap, and parks the call once parkAfter replies counted. A parked call
// stays until it expires, and Pending skips it, so the calls behind it flow.
// The gap keeps a burst of passing errors from parking a call at once.
func (s *Store) Fail(ctx context.Context, seq int64, parkAfter int, minGap time.Duration) (parked bool, err error) {
	n, err := s.q.FailOrchOutbox(ctx, gen.FailOrchOutboxParams{
		Now: s.now().UnixNano(), ParkAfter: int64(parkAfter), Seq: seq, MinGap: minGap.Nanoseconds(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n == 1, err
}

// Ack removes a sent call.
func (s *Store) Ack(ctx context.Context, seq int64) error {
	return s.q.DeleteOrchOutbox(ctx, seq)
}

// DropDeliver removes a deliver of message id still queued for node and
// reports whether there was one. A deliver already in flight may still land.
func (s *Store) DropDeliver(ctx context.Context, node, id string) (bool, error) {
	n, err := s.q.DropOrchOutboxDeliver(ctx, gen.DropOrchOutboxDeliverParams{Node: node, MessageID: id})
	return n > 0, err
}

// PendingNodes returns the nodes with queued calls, sorted.
func (s *Store) PendingNodes(ctx context.Context) ([]string, error) {
	return s.q.OrchOutboxNodes(ctx)
}

// ExpireOutbox deletes the calls queued before the cutoff and returns how many.
func (s *Store) ExpireOutbox(ctx context.Context, before time.Time) (int64, error) {
	return s.q.ExpireOrchOutbox(ctx, before.UnixNano())
}

// ReplyAndEnqueue is ReplyTo for a reply to an actor on node, another node:
// it also queues the reply's deliver to node, in the same transaction.
func (s *Store) ReplyAndEnqueue(ctx context.Context, origID string, m api.Message, node string) (api.Message, bool, error) {
	m, open, err := s.reply(ctx, origID, m, func(q *gen.Queries, m api.Message) error {
		return s.enqueue(ctx, q, node, api.MethodOrchPeerDeliver, api.OrchPeerDeliverParams{Message: m})
	})
	if err == nil && open {
		s.signalEnqueued()
	}
	return m, open, err
}
