// Package mesh persists the agent message queue and parent links.
package mesh

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/db/gen"
	"github.com/MunifTanjim/argus/internal/session"
)

// NewID returns a message ID of the form m-xxxxxxxx.
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "m-" + hex.EncodeToString(b[:])
}

type Store struct {
	db  *sql.DB
	q   *gen.Queries
	now func() time.Time

	// linksMu serializes link writes with links, a child key -> link mirror of
	// mesh_link loaded on first Link; nil until then.
	linksMu sync.Mutex
	links   map[string]api.Link
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	return &Store{db: db, q: gen.New(db), now: now}
}

// Insert stores m, assigning an ID when empty and setting both timestamps.
func (s *Store) Insert(ctx context.Context, m api.Message) (api.Message, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	m.CreatedAt = s.now()
	m.UpdatedAt = m.CreatedAt
	interaction := ""
	if m.Interaction != nil {
		b, err := json.Marshal(m.Interaction)
		if err != nil {
			return api.Message{}, fmt.Errorf("mesh: encode interaction: %w", err)
		}
		interaction = string(b)
	}
	err := s.q.InsertMeshMessage(ctx, gen.InsertMeshMessageParams{
		ID:            m.ID,
		Kind:          string(m.Kind),
		SenderNode:    m.From.Node,
		SenderKey:     m.From.Key,
		RecipientNode: m.To.Node,
		RecipientKey:  m.To.Key,
		Body:          m.Body,
		ReplyTo:       m.ReplyTo,
		SubjectKey:    m.Subject,
		Interaction:   interaction,
		State:         string(m.State),
		AnsweredBy:    m.AnsweredBy,
		CreatedAt:     m.CreatedAt.UnixNano(),
		UpdatedAt:     m.UpdatedAt.UnixNano(),
	})
	if err != nil {
		return api.Message{}, fmt.Errorf("mesh: insert: %w", err)
	}
	return m, nil
}

func (s *Store) Get(ctx context.Context, id string) (api.Message, bool, error) {
	row, err := s.q.GetMeshMessage(ctx, id)
	return one(row, err)
}

func (s *Store) NextQueued(ctx context.Context, recipientKey string) (api.Message, bool, error) {
	row, err := s.q.NextQueuedMeshMessage(ctx, recipientKey)
	return one(row, err)
}

// Queued returns every queued message for recipientKey, oldest first.
func (s *Store) Queued(ctx context.Context, recipientKey string) ([]api.Message, error) {
	rows, err := s.q.QueuedMeshMessages(ctx, recipientKey)
	return many(rows, err)
}

func (s *Store) QueuedRecipients(ctx context.Context) ([]string, error) {
	return s.q.QueuedMeshRecipients(ctx)
}

func (s *Store) SetState(ctx context.Context, id string, st api.MessageState) error {
	return s.q.SetMeshMessageState(ctx, gen.SetMeshMessageStateParams{
		State:      string(st),
		AnsweredBy: "",
		UpdatedAt:  s.now().UnixNano(),
		ID:         id,
	})
}

// Transition moves a message from one state to another and reports whether it
// was in the from state.
func (s *Store) Transition(ctx context.Context, id string, from, to api.MessageState) (bool, error) {
	n, err := s.q.TransitionMeshMessageState(ctx, gen.TransitionMeshMessageStateParams{
		ToState:   string(to),
		FromState: string(from),
		UpdatedAt: s.now().UnixNano(),
		ID:        id,
	})
	return n > 0, err
}

// FailQueued marks every queued message for recipientKey failed and returns how many.
func (s *Store) FailQueued(ctx context.Context, recipientKey string) (int64, error) {
	return s.q.FailQueuedMeshMessages(ctx, gen.FailQueuedMeshMessagesParams{
		UpdatedAt:    s.now().UnixNano(),
		RecipientKey: recipientKey,
	})
}

// OpenNotices returns prompt notices about subjectKey that are queued or delivered.
func (s *Store) OpenNotices(ctx context.Context, subjectKey string) ([]api.Message, error) {
	rows, err := s.q.OpenMeshNotices(ctx, subjectKey)
	return many(rows, err)
}

// UnrepliedTask returns the latest delivered task for childKey that has no reply yet.
func (s *Store) UnrepliedTask(ctx context.Context, childKey string) (api.Message, bool, error) {
	row, err := s.q.UnrepliedMeshTask(ctx, childKey)
	return one(row, err)
}

// List returns up to limit messages, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]api.Message, error) {
	rows, err := s.q.ListMeshMessages(ctx, int64(limit))
	return many(rows, err)
}

// Rekey replaces oldKey with newKey in every message and link, atomically.
func (s *Store) Rekey(ctx context.Context, oldKey, newKey string) error {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mesh: rekey: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	steps := []func() error{
		func() error {
			return q.RekeyMeshSender(ctx, gen.RekeyMeshSenderParams{NewKey: newKey, OldKey: oldKey})
		},
		func() error {
			return q.RekeyMeshRecipient(ctx, gen.RekeyMeshRecipientParams{NewKey: newKey, OldKey: oldKey})
		},
		func() error {
			return q.RekeyMeshSubject(ctx, gen.RekeyMeshSubjectParams{NewKey: newKey, OldKey: oldKey})
		},
		func() error {
			return q.RekeyMeshLinkChild(ctx, gen.RekeyMeshLinkChildParams{NewKey: newKey, OldKey: oldKey})
		},
		func() error {
			return q.RekeyMeshLinkParent(ctx, gen.RekeyMeshLinkParentParams{NewKey: newKey, OldKey: oldKey})
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("mesh: rekey: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.links == nil {
		return nil
	}
	if l, ok := s.links[oldKey]; ok {
		delete(s.links, oldKey)
		l.Child.Key = newKey
		s.links[newKey] = l
	}
	for k, l := range s.links {
		if l.Parent.Key == oldKey {
			l.Parent.Key = newKey
			s.links[k] = l
		}
	}
	return nil
}

// Expire deletes messages created before the cutoff and returns how many.
func (s *Store) Expire(ctx context.Context, before time.Time) (int64, error) {
	return s.q.ExpireMeshMessages(ctx, before.UnixNano())
}

// PutLink inserts or replaces the link for l.Child.Key, keeping its original creation time.
func (s *Store) PutLink(ctx context.Context, l api.Link) error {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	err := s.q.PutMeshLink(ctx, gen.PutMeshLinkParams{
		ChildKey:   l.Child.Key,
		ChildNode:  l.Child.Node,
		ParentNode: l.Parent.Node,
		ParentKey:  l.Parent.Key,
		TaskID:     l.TaskID,
		EndedBy:    l.EndedBy,
		CreatedAt:  s.now().UnixNano(),
	})
	if err == nil && s.links != nil {
		s.links[l.Child.Key] = l
	}
	return err
}

func (s *Store) Link(ctx context.Context, childKey string) (api.Link, bool, error) {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	if s.links == nil {
		rows, err := s.q.ListMeshLinks(ctx)
		if err != nil {
			return api.Link{}, false, err
		}
		s.links = make(map[string]api.Link, len(rows))
		for _, r := range rows {
			s.links[r.ChildKey] = toLink(r)
		}
	}
	l, ok := s.links[childKey]
	return l, ok, nil
}

func (s *Store) Links(ctx context.Context) ([]api.Link, error) {
	rows, err := s.q.ListMeshLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.Link, 0, len(rows))
	for _, r := range rows {
		out = append(out, toLink(r))
	}
	return out, nil
}

func (s *Store) SetEndedBy(ctx context.Context, childKey, by string) error {
	s.linksMu.Lock()
	defer s.linksMu.Unlock()
	err := s.q.SetMeshLinkEndedBy(ctx, gen.SetMeshLinkEndedByParams{EndedBy: by, ChildKey: childKey})
	if l, ok := s.links[childKey]; err == nil && ok {
		l.EndedBy = by
		s.links[childKey] = l
	}
	return err
}

func toLink(r gen.MeshLink) api.Link {
	return api.Link{
		Child:   api.Party{Node: r.ChildNode, Key: r.ChildKey},
		Parent:  api.Party{Node: r.ParentNode, Key: r.ParentKey},
		TaskID:  r.TaskID,
		EndedBy: r.EndedBy,
	}
}

func one(row gen.MeshMessage, err error) (api.Message, bool, error) {
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

func many(rows []gen.MeshMessage, err error) ([]api.Message, error) {
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

func toMessage(r gen.MeshMessage) (api.Message, error) {
	m := api.Message{
		ID:         r.ID,
		Kind:       api.MessageKind(r.Kind),
		From:       api.Party{Node: r.SenderNode, Key: r.SenderKey},
		To:         api.Party{Node: r.RecipientNode, Key: r.RecipientKey},
		Body:       r.Body,
		ReplyTo:    r.ReplyTo,
		Subject:    r.SubjectKey,
		State:      api.MessageState(r.State),
		AnsweredBy: r.AnsweredBy,
		CreatedAt:  time.Unix(0, r.CreatedAt),
		UpdatedAt:  time.Unix(0, r.UpdatedAt),
	}
	if r.Interaction != "" {
		var in session.Interaction
		if err := json.Unmarshal([]byte(r.Interaction), &in); err != nil {
			return api.Message{}, fmt.Errorf("mesh: decode interaction: %w", err)
		}
		m.Interaction = &in
	}
	return m, nil
}
