// Package orch persists the agent message queue and parent links.
package orch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db/gen"
	"github.com/MunifTanjim/argus/internal/session"
)

// NewID returns a message ID of the form nodeID:m- and 16 hex characters.
func NewID(nodeID string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return nodeID + ":m-" + hex.EncodeToString(b[:])
}

type Store struct {
	db  *sql.DB
	q   *gen.Queries
	now func() time.Time

	nodeMu sync.RWMutex
	nodeID string // prefix of IDs Insert assigns

	// linksMu serializes link writes with links, a child key -> link mirror of
	// orch_link loaded on first Link; nil until then.
	linksMu sync.Mutex
	links   map[api.Actor]api.Link
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	return &Store{db: db, q: gen.New(db), now: now}
}

// SetNodeID sets the node ID that prefixes message IDs assigned by Insert.
func (s *Store) SetNodeID(id string) {
	s.nodeMu.Lock()
	s.nodeID = id
	s.nodeMu.Unlock()
}

func (s *Store) newID() string {
	s.nodeMu.RLock()
	defer s.nodeMu.RUnlock()
	return NewID(s.nodeID)
}

// Request is a caller's request_id and the hash of the request parameters.
type Request struct {
	ID   string
	Hash string
}

// Insert stores m, assigning an ID when empty and setting both timestamps.
func (s *Store) Insert(ctx context.Context, m api.Message) (api.Message, error) {
	return s.insert(ctx, s.q, m, Request{})
}

// InsertRequest is Insert for a message sent with a request_id.
func (s *Store) InsertRequest(ctx context.Context, m api.Message, req Request) (api.Message, error) {
	return s.insert(ctx, s.q, m, req)
}

func (s *Store) insert(ctx context.Context, q *gen.Queries, m api.Message, req Request) (api.Message, error) {
	if m.ID == "" {
		m.ID = s.newID()
	}
	m.CreatedAt = s.now()
	m.UpdatedAt = m.CreatedAt
	interaction, err := encodeInteraction(m)
	if err != nil {
		return api.Message{}, err
	}
	childNode, childKey := actorCols(m.Child)
	answeredNode, answeredKey := actorCols(m.AnsweredBy)
	err = q.InsertOrchMessage(ctx, gen.InsertOrchMessageParams{
		ID:             m.ID,
		Kind:           string(m.Kind),
		FromNode:       m.From.Node,
		FromKey:        m.From.Key,
		ToNode:         m.To.Node,
		ToKey:          m.To.Key,
		Body:           m.Body,
		ReplyTo:        m.ReplyTo,
		ChildNode:      childNode,
		ChildKey:       childKey,
		Interaction:    interaction,
		State:          string(m.State),
		AnsweredByNode: answeredNode,
		AnsweredByKey:  answeredKey,
		RequestID:      req.ID,
		RequestHash:    req.Hash,
		CreatedAt:      m.CreatedAt.UnixNano(),
		UpdatedAt:      m.UpdatedAt.UnixNano(),
	})
	if err != nil {
		return api.Message{}, fmt.Errorf("orch: insert: %w", err)
	}
	return m, nil
}

// actorCols splits an optional actor into its node and key columns.
func actorCols(a *api.Actor) (node, key string) {
	if a == nil {
		return "", ""
	}
	return a.Node, a.Key
}

// colsActor is actorCols reversed: an empty key is no actor.
func colsActor(node, key string) *api.Actor {
	if key == "" {
		return nil
	}
	return &api.Actor{Node: node, Key: key}
}

func encodeInteraction(m api.Message) (string, error) {
	if m.Interaction == nil {
		return "", nil
	}
	b, err := json.Marshal(m.Interaction)
	if err != nil {
		return "", fmt.Errorf("orch: encode interaction: %w", err)
	}
	return string(b), nil
}

func (s *Store) Get(ctx context.Context, id string) (api.Message, bool, error) {
	row, err := s.q.GetOrchMessage(ctx, id)
	return one(row, err)
}

// MessageByRequest returns the message sender sent with requestID and its request hash.
func (s *Store) MessageByRequest(ctx context.Context, sender api.Actor, requestID string) (api.Message, string, bool, error) {
	row, err := s.q.GetOrchMessageByRequest(ctx, gen.GetOrchMessageByRequestParams{
		FromNode: sender.Node, FromKey: sender.Key, RequestID: requestID,
	})
	m, ok, err := one(row, err)
	return m, row.RequestHash, ok, err
}

// Queued returns the queued messages for to, oldest first.
func (s *Store) Queued(ctx context.Context, to api.Actor) ([]api.Message, error) {
	rows, err := s.q.QueuedOrchMessages(ctx, gen.QueuedOrchMessagesParams{ToNode: to.Node, ToKey: to.Key})
	return many(rows, err)
}

// QueuedRecipients returns the keys of node's actors with queued messages.
func (s *Store) QueuedRecipients(ctx context.Context, node string) ([]string, error) {
	return s.q.QueuedOrchTo(ctx, node)
}

func (s *Store) SetState(ctx context.Context, id string, st api.MessageState) error {
	return s.q.SetOrchMessageState(ctx, gen.SetOrchMessageStateParams{
		State:     string(st),
		UpdatedAt: s.now().UnixNano(),
		ID:        id,
	})
}

// Transition moves a message from one state to another and reports whether it
// was in the from state.
func (s *Store) Transition(ctx context.Context, id string, from, to api.MessageState) (bool, error) {
	n, err := s.q.TransitionOrchMessageState(ctx, gen.TransitionOrchMessageStateParams{
		ToState:   string(to),
		FromState: string(from),
		UpdatedAt: s.now().UnixNano(),
		ID:        id,
	})
	return n > 0, err
}

// TransitionAndInsert moves message id from one state to another and, when it
// was in the from state, stores m as Insert does, in one transaction. It
// reports whether the message was in the from state; on error, neither is kept.
func (s *Store) TransitionAndInsert(ctx context.Context, id string, from, to api.MessageState, m api.Message) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("orch: transition and insert: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	n, err := q.TransitionOrchMessageState(ctx, gen.TransitionOrchMessageStateParams{
		ToState: string(to), FromState: string(from), UpdatedAt: s.now().UnixNano(), ID: id,
	})
	if err != nil || n == 0 {
		return false, err
	}
	if _, err := s.insert(ctx, q, m, Request{}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("orch: transition and insert: %w", err)
	}
	return true, nil
}

// ClaimInteraction marks an open interaction message replied by the actor by and
// returns the state it was in, or false when it was not open.
func (s *Store) ClaimInteraction(ctx context.Context, id string, by api.Actor) (api.MessageState, bool, error) {
	// queued first: delivery only moves a notice forward to delivered.
	for _, from := range []api.MessageState{api.MessageQueued, api.MessageDelivered} {
		n, err := s.q.ClaimOrchInteraction(ctx, gen.ClaimOrchInteractionParams{
			AnsweredByNode: by.Node,
			AnsweredByKey:  by.Key,
			UpdatedAt:      s.now().UnixNano(),
			ID:             id,
			FromState:      string(from),
		})
		if err != nil {
			return "", false, err
		}
		if n > 0 {
			return from, true, nil
		}
	}
	return "", false, nil
}

// ReleaseInteraction undoes ClaimInteraction by the actor by, moving the notice to st.
func (s *Store) ReleaseInteraction(ctx context.Context, id string, by api.Actor, st api.MessageState) (bool, error) {
	n, err := s.q.ReleaseOrchInteraction(ctx, gen.ReleaseOrchInteractionParams{
		ToState:        string(st),
		UpdatedAt:      s.now().UnixNano(),
		ID:             id,
		AnsweredByNode: by.Node,
		AnsweredByKey:  by.Key,
	})
	return n > 0, err
}

// FailQueued marks the queued messages for to failed and returns how many.
// Argus notices stay queued: a parent that comes back still gets them.
func (s *Store) FailQueued(ctx context.Context, to api.Actor) (int64, error) {
	return s.q.FailQueuedOrchMessages(ctx, gen.FailQueuedOrchMessagesParams{
		UpdatedAt: s.now().UnixNano(),
		ToNode:    to.Node,
		ToKey:     to.Key,
	})
}

// OpenInteractions returns interaction messages about child that are queued or delivered.
func (s *Store) OpenInteractions(ctx context.Context, child api.Actor) ([]api.Message, error) {
	rows, err := s.q.OpenOrchInteractions(ctx, gen.OpenOrchInteractionsParams{ChildNode: child.Node, ChildKey: child.Key})
	return many(rows, err)
}

// UnrepliedTask returns the latest delivered task for child that has no reply yet.
func (s *Store) UnrepliedTask(ctx context.Context, child api.Actor) (api.Message, bool, error) {
	row, err := s.q.UnrepliedOrchTask(ctx, gen.UnrepliedOrchTaskParams{ToNode: child.Node, ToKey: child.Key})
	return one(row, err)
}

// List returns up to limit messages, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]api.Message, error) {
	rows, err := s.q.ListOrchMessages(ctx, int64(limit))
	return many(rows, err)
}

// Rekey moves this node's actor oldKey to newKey in every message and link,
// atomically. An actor of another node with the same key is left as is.
func (s *Store) Rekey(ctx context.Context, oldKey, newKey string) error {
	s.nodeMu.RLock()
	self := s.nodeID
	s.nodeMu.RUnlock()
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	err := s.inTx(ctx, "rekey", func(q *gen.Queries) error {
		return rekeyNodeRows(ctx, q, self, oldKey, newKey)
	})
	if err != nil {
		return err
	}
	s.rekeyLinks(oldKey, newKey, func(p api.Actor) bool { return p.Node == self })
	return nil
}

// rekeyNodeRows moves node's actor oldKey to newKey in every message and link.
func rekeyNodeRows(ctx context.Context, q *gen.Queries, node, oldKey, newKey string) error {
	return firstErr(
		func() error {
			return q.RekeyOrchNodeSender(ctx, gen.RekeyOrchNodeSenderParams{NewKey: newKey, Node: node, OldKey: oldKey})
		},
		func() error {
			return q.RekeyOrchNodeRecipient(ctx, gen.RekeyOrchNodeRecipientParams{NewKey: newKey, Node: node, OldKey: oldKey})
		},
		func() error {
			return q.RekeyOrchNodeChild(ctx, gen.RekeyOrchNodeChildParams{NewKey: newKey, Node: node, OldKey: oldKey})
		},
		func() error {
			return q.RekeyOrchNodeLinkChild(ctx, gen.RekeyOrchNodeLinkChildParams{NewKey: newKey, Node: node, OldKey: oldKey})
		},
		func() error {
			return q.RekeyOrchNodeLinkParent(ctx, gen.RekeyOrchNodeLinkParentParams{NewKey: newKey, Node: node, OldKey: oldKey})
		},
	)
}

// inTx runs fn in one transaction and commits when fn succeeds.
func (s *Store) inTx(ctx context.Context, op string, fn func(q *gen.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("orch: %s: %w", op, err)
	}
	defer tx.Rollback()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return fmt.Errorf("orch: %s: %w", op, err)
	}
	return tx.Commit()
}

func firstErr(steps ...func() error) error {
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// rekeyLinks patches the link mirror after a rekey of the actors that match.
// The caller holds linksMu.
func (s *Store) rekeyLinks(oldKey, newKey string, match func(api.Actor) bool) {
	if s.links == nil {
		return
	}
	for k, l := range s.links {
		if k.Key == oldKey && match(k) {
			delete(s.links, k)
			l.Child.Key = newKey
			s.links[l.Child] = l
		}
	}
	for k, l := range s.links {
		if l.Parent.Key == oldKey && match(l.Parent) {
			l.Parent.Key = newKey
			s.links[k] = l
		}
	}
}

// Expire deletes messages created before the cutoff and returns how many.
func (s *Store) Expire(ctx context.Context, before time.Time) (int64, error) {
	return s.q.ExpireOrchMessages(ctx, before.UnixNano())
}

// ExpireLinks deletes the links created before the cutoff whose child is not
// live, and returns how many.
func (s *Store) ExpireLinks(ctx context.Context, before time.Time, live func(child api.Actor) bool) (int, error) {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	rows, err := s.q.ListOrchLinks(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		child := api.Actor{Node: r.ChildNode, Key: r.ChildKey}
		if r.CreatedAt >= before.UnixNano() || live(child) {
			continue
		}
		if err := s.q.DeleteOrchLink(ctx, gen.DeleteOrchLinkParams{ChildNode: r.ChildNode, ChildKey: r.ChildKey}); err != nil {
			return n, err
		}
		delete(s.links, child)
		n++
	}
	return n, nil
}

// PutLink inserts or replaces the link for l.Child, keeping its original creation time.
func (s *Store) PutLink(ctx context.Context, l api.Link) error {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	if err := s.putLink(ctx, s.q, l, Request{}); err != nil {
		return err
	}
	s.mirrorLink(l)
	return nil
}

// SpawnedLink is a link written for a spawn with a request_id.
type SpawnedLink struct {
	Link api.Link
	Hash string
}

// LinkSpawned stores the link of a spawned child and the child's task when
// task.ID is set, in one transaction.
func (s *Store) LinkSpawned(ctx context.Context, l api.Link, task api.Message, req Request) error {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("orch: link spawned: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	if err := s.putLink(ctx, q, l, req); err != nil {
		return err
	}
	if task.ID != "" {
		if _, err := s.insert(ctx, q, task, Request{}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("orch: link spawned: %w", err)
	}
	s.mirrorLink(l)
	return nil
}

// LinkByRequest returns the link parent created with requestID.
func (s *Store) LinkByRequest(ctx context.Context, parent api.Actor, requestID string) (SpawnedLink, bool, error) {
	row, err := s.q.GetOrchLinkByRequest(ctx, gen.GetOrchLinkByRequestParams{
		ParentNode: parent.Node, ParentKey: parent.Key, RequestID: requestID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return SpawnedLink{}, false, nil
	}
	if err != nil {
		return SpawnedLink{}, false, err
	}
	return SpawnedLink{Link: toLink(row), Hash: row.RequestHash}, true, nil
}

func (s *Store) putLink(ctx context.Context, q *gen.Queries, l api.Link, req Request) error {
	return q.PutOrchLink(ctx, gen.PutOrchLinkParams{
		ChildKey:       l.Child.Key,
		ChildNode:      l.Child.Node,
		ParentNode:     l.Parent.Node,
		ParentKey:      l.Parent.Key,
		TaskID:         l.TaskID,
		EndedBy:        l.EndedBy,
		RequestID:      req.ID,
		RequestHash:    req.Hash,
		ChildSessionID: l.ChildSessionID,
		CreatedAt:      s.now().UnixNano(),
	})
}

func (s *Store) mirrorLink(l api.Link) {
	if s.links != nil {
		s.links[l.Child] = l
	}
}

// Link returns the link of child.
func (s *Store) Link(ctx context.Context, child api.Actor) (api.Link, bool, error) {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	if err := s.loadLinks(ctx); err != nil {
		return api.Link{}, false, err
	}
	l, ok := s.links[child]
	return l, ok, nil
}

// Children returns the links whose parent is parent, ordered by child.
func (s *Store) Children(ctx context.Context, parent api.Actor) ([]api.Link, error) {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	if err := s.loadLinks(ctx); err != nil {
		return nil, err
	}
	var out []api.Link
	for _, l := range s.links {
		if l.Parent == parent {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, compareChild)
	return out, nil
}

func (s *Store) loadLinks(ctx context.Context) error {
	if s.links != nil {
		return nil
	}
	rows, err := s.q.ListOrchLinks(ctx)
	if err != nil {
		return err
	}
	s.links = make(map[api.Actor]api.Link, len(rows))
	for _, r := range rows {
		l := toLink(r)
		s.links[l.Child] = l
	}
	return nil
}

// Links returns every link, ordered by child.
func (s *Store) Links(ctx context.Context) ([]api.Link, error) {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	if err := s.loadLinks(ctx); err != nil {
		return nil, err
	}
	out := make([]api.Link, 0, len(s.links))
	for _, l := range s.links {
		out = append(out, l)
	}
	slices.SortFunc(out, compareChild)
	return out, nil
}

// compareChild orders links by child node, then child key.
func compareChild(a, b api.Link) int {
	if c := strings.Compare(a.Child.Node, b.Child.Node); c != 0 {
		return c
	}
	return strings.Compare(a.Child.Key, b.Child.Key)
}

// SetEndedBy records how child ended.
func (s *Store) SetEndedBy(ctx context.Context, child api.Actor, by string) error {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	err := s.q.SetOrchLinkEndedBy(ctx, gen.SetOrchLinkEndedByParams{EndedBy: by, ChildNode: child.Node, ChildKey: child.Key})
	if l, ok := s.links[child]; err == nil && ok {
		l.EndedBy = by
		s.links[child] = l
	}
	return err
}

func toLink(r gen.OrchLink) api.Link {
	return api.Link{
		Child:          api.Actor{Node: r.ChildNode, Key: r.ChildKey},
		Parent:         api.Actor{Node: r.ParentNode, Key: r.ParentKey},
		TaskID:         r.TaskID,
		EndedBy:        r.EndedBy,
		ChildSessionID: r.ChildSessionID,
	}
}

func one(row gen.OrchMessage, err error) (api.Message, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return api.Message{}, false, nil
	}
	if err != nil {
		return api.Message{}, false, err
	}
	m, err := toMessage(row)
	if err != nil {
		return api.Message{}, false, err
	}
	return m, true, nil
}

func many(rows []gen.OrchMessage, err error) ([]api.Message, error) {
	if err != nil {
		return nil, err
	}
	out := make([]api.Message, 0, len(rows))
	for _, r := range rows {
		m, err := toMessage(r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func toMessage(r gen.OrchMessage) (api.Message, error) {
	m := api.Message{
		ID:         r.ID,
		Kind:       api.MessageKind(r.Kind),
		From:       api.Actor{Node: r.FromNode, Key: r.FromKey},
		To:         api.Actor{Node: r.ToNode, Key: r.ToKey},
		Body:       r.Body,
		ReplyTo:    r.ReplyTo,
		Child:      colsActor(r.ChildNode, r.ChildKey),
		State:      api.MessageState(r.State),
		AnsweredBy: colsActor(r.AnsweredByNode, r.AnsweredByKey),
		CreatedAt:  time.Unix(0, r.CreatedAt),
		UpdatedAt:  time.Unix(0, r.UpdatedAt),
	}
	if r.Interaction != "" {
		var in session.Interaction
		if err := json.Unmarshal([]byte(r.Interaction), &in); err != nil {
			return api.Message{}, fmt.Errorf("orch: decode interaction: %w", err)
		}
		m.Interaction = &in
	}
	return m, nil
}

// ReplyTo stores the reply m to the message origID and marks origID replied,
// in one transaction. origID must be queued, delivered, or already replied: a
// failed or withdrawn message stays as it is. ReplyTo reports whether origID
// was open.
func (s *Store) ReplyTo(ctx context.Context, origID string, m api.Message) (api.Message, bool, error) {
	return s.reply(ctx, origID, m, nil)
}

// reply is ReplyTo with then run in the same transaction after the insert.
func (s *Store) reply(ctx context.Context, origID string, m api.Message, then func(q *gen.Queries, m api.Message) error) (api.Message, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return api.Message{}, false, fmt.Errorf("orch: reply: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	open := false
	for _, from := range []api.MessageState{api.MessageQueued, api.MessageDelivered, api.MessageReplied} {
		n, err := q.TransitionOrchMessageState(ctx, gen.TransitionOrchMessageStateParams{
			ToState: string(api.MessageReplied), FromState: string(from), UpdatedAt: s.now().UnixNano(), ID: origID,
		})
		if err != nil {
			return api.Message{}, false, fmt.Errorf("orch: reply: %w", err)
		}
		if n > 0 {
			open = true
			break
		}
	}
	if !open {
		return api.Message{}, false, nil
	}
	if m, err = s.insert(ctx, q, m, Request{}); err != nil {
		return api.Message{}, false, err
	}
	if then != nil {
		if err := then(q, m); err != nil {
			return api.Message{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return api.Message{}, false, fmt.Errorf("orch: reply: %w", err)
	}
	return m, true, nil
}
