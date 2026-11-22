package pagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

// Store persists agents, their threads, sessions and events.
type Store struct {
	db  *sql.DB
	q   *gen.Queries
	now func() time.Time
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	return &Store{db: db, q: gen.New(db), now: now}
}

type Agent struct {
	ID, Name, Dir         string
	ArchivedAt, CreatedAt time.Time
}

type Thread struct {
	ID, AgentID, Kind, StartedBy, Title, Status string
	ReadPosition                                int64
	CreatedAt, LastActiveAt                     time.Time
}

type ThreadSession struct {
	ThreadID, SessionID, CLI, AgentSessionID, TranscriptPath, StartedBy, EndReason string
	StartedAt, EndedAt                                                             time.Time
}

type Event struct {
	ID            int64
	ThreadID      string
	Kind          string
	StartedBy     string
	Payload       json.RawMessage
	AnchorSession string
	AnchorIndex   int
	CreatedAt     time.Time
}

const (
	ThreadActive = "active"
	ThreadClosed = "closed"
)

// ErrNotFound is returned when an update names a row that does not exist.
var ErrNotFound = errors.New("pagent: not found")

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

func agentFrom(r gen.Pagent) Agent {
	return Agent{ID: r.ID, Name: r.Name, Dir: r.Dir, ArchivedAt: fromNanos(r.ArchivedAt), CreatedAt: fromNanos(r.CreatedAt)}
}

func threadFrom(r gen.PagentThread) Thread {
	return Thread{
		ID: r.ID, AgentID: r.AgentID, Kind: r.Kind, StartedBy: r.StartedBy, Title: r.Title, Status: r.Status,
		ReadPosition: r.ReadPosition, CreatedAt: fromNanos(r.CreatedAt), LastActiveAt: fromNanos(r.LastActiveAt),
	}
}

func one[R, T any](r R, err error, conv func(R) T) (T, bool, error) {
	var zero T
	if errors.Is(err, sql.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, err
	}
	return conv(r), true, nil
}

func affected(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertAgent stores a, setting CreatedAt.
func (s *Store) InsertAgent(ctx context.Context, a Agent) error {
	return s.q.InsertPAgent(ctx, gen.InsertPAgentParams{ID: a.ID, Name: a.Name, Dir: a.Dir, CreatedAt: s.now().UnixNano()})
}

// AgentByName finds an active agent.
func (s *Store) AgentByName(ctx context.Context, name string) (Agent, bool, error) {
	r, err := s.q.GetPAgentByName(ctx, name)
	return one(r, err, agentFrom)
}

func (s *Store) AgentByID(ctx context.Context, id string) (Agent, bool, error) {
	r, err := s.q.GetPAgentByID(ctx, id)
	return one(r, err, agentFrom)
}

func (s *Store) AgentByDir(ctx context.Context, dir string) (Agent, bool, error) {
	r, err := s.q.GetPAgentByDir(ctx, dir)
	return one(r, err, agentFrom)
}

func (s *Store) ListAgents(ctx context.Context, includeArchived bool) ([]Agent, error) {
	list := s.q.ListActivePAgents
	if includeArchived {
		list = s.q.ListAllPAgents
	}
	rows, err := list(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Agent, len(rows))
	for i, r := range rows {
		out[i] = agentFrom(r)
	}
	return out, nil
}

func (s *Store) RenameAgent(ctx context.Context, id, name string) error {
	return affected(s.q.RenamePAgent(ctx, gen.RenamePAgentParams{Name: name, ID: id}))
}

func (s *Store) ArchiveAgent(ctx context.Context, id string) error {
	return affected(s.q.SetPAgentArchivedAt(ctx, gen.SetPAgentArchivedAtParams{ArchivedAt: s.now().UnixNano(), ID: id}))
}

// RestoreAgent fails when an active agent already has the name.
func (s *Store) RestoreAgent(ctx context.Context, id string) error {
	a, ok, err := s.AgentByID(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := affected(s.q.SetPAgentArchivedAt(ctx, gen.SetPAgentArchivedAtParams{ArchivedAt: 0, ID: id})); err != nil {
		if _, taken, _ := s.AgentByName(ctx, a.Name); taken {
			return fmt.Errorf("agent name %s is taken", a.Name)
		}
		return err
	}
	return nil
}

// PurgeAgent deletes the agent and everything stored for it.
func (s *Store) PurgeAgent(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pagent: purge: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	for _, del := range []func(context.Context, string) error{
		q.DeletePAgentEvents, q.DeletePAgentCursors, q.DeletePAgentSessions,
		q.DeletePAgentProposals, q.DeletePAgentThreads, q.DeletePAgent,
	} {
		if err := del(ctx, id); err != nil {
			return fmt.Errorf("pagent: purge: %w", err)
		}
	}
	return tx.Commit()
}

// InsertThread stores t, setting both timestamps and defaulting Status to active.
func (s *Store) InsertThread(ctx context.Context, t Thread) error {
	if t.Status == "" {
		t.Status = ThreadActive
	}
	now := s.now().UnixNano()
	return s.q.InsertPAgentThread(ctx, gen.InsertPAgentThreadParams{
		ID: t.ID, AgentID: t.AgentID, Kind: t.Kind, StartedBy: t.StartedBy, Title: t.Title, Status: t.Status,
		CreatedAt: now, LastActiveAt: now,
	})
}

// LatestThread returns the active thread of kind most recently active.
func (s *Store) LatestThread(ctx context.Context, agentID, kind string) (Thread, bool, error) {
	r, err := s.q.LatestPAgentThread(ctx, gen.LatestPAgentThreadParams{AgentID: agentID, Kind: kind})
	return one(r, err, threadFrom)
}

// ListThreads returns all threads of the agent, most recently active first.
func (s *Store) ListThreads(ctx context.Context, agentID string) ([]Thread, error) {
	rows, err := s.q.ListPAgentThreads(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]Thread, len(rows))
	for i, r := range rows {
		out[i] = threadFrom(r)
	}
	return out, nil
}

func (s *Store) TouchThread(ctx context.Context, id string) error {
	return affected(s.q.TouchPAgentThread(ctx, gen.TouchPAgentThreadParams{LastActiveAt: s.now().UnixNano(), ID: id}))
}

func (s *Store) SetReadPosition(ctx context.Context, id string, pos int64) error {
	return affected(s.q.SetPAgentThreadReadPosition(ctx, gen.SetPAgentThreadReadPositionParams{ReadPosition: pos, ID: id}))
}

func (s *Store) CloseThread(ctx context.Context, id string) error {
	return affected(s.q.SetPAgentThreadStatus(ctx, gen.SetPAgentThreadStatusParams{Status: ThreadClosed, ID: id}))
}

// InsertThreadSession stores ts, setting StartedAt.
func (s *Store) InsertThreadSession(ctx context.Context, ts ThreadSession) error {
	return s.q.InsertPAgentThreadSession(ctx, gen.InsertPAgentThreadSessionParams{
		ThreadID: ts.ThreadID, SessionID: ts.SessionID, Cli: ts.CLI, AgentSessionID: ts.AgentSessionID,
		TranscriptPath: ts.TranscriptPath, StartedBy: ts.StartedBy, StartedAt: s.now().UnixNano(),
	})
}

func (s *Store) SetAgentSessionID(ctx context.Context, sessionID, cli, agentSessionID, transcriptPath string) error {
	return affected(s.q.SetPAgentSessionAgentID(ctx, gen.SetPAgentSessionAgentIDParams{
		Cli: cli, AgentSessionID: agentSessionID, TranscriptPath: transcriptPath, SessionID: sessionID,
	}))
}

// EndThreadSession records that the session ended; an already ended session is left as is.
func (s *Store) EndThreadSession(ctx context.Context, sessionID, reason string) error {
	n, err := s.q.EndPAgentThreadSession(ctx, gen.EndPAgentThreadSessionParams{
		EndedAt: s.now().UnixNano(), EndReason: reason, SessionID: sessionID,
	})
	if err != nil || n > 0 {
		return err
	}
	if _, ok, err := s.ThreadBySession(ctx, sessionID); err != nil || !ok {
		if err == nil {
			err = ErrNotFound
		}
		return err
	}
	return nil
}

func (s *Store) ThreadSessions(ctx context.Context, threadID string) ([]ThreadSession, error) {
	rows, err := s.q.ListPAgentThreadSessions(ctx, threadID)
	if err != nil {
		return nil, err
	}
	out := make([]ThreadSession, len(rows))
	for i, r := range rows {
		out[i] = ThreadSession{
			ThreadID: r.ThreadID, SessionID: r.SessionID, CLI: r.Cli, AgentSessionID: r.AgentSessionID,
			TranscriptPath: r.TranscriptPath, StartedBy: r.StartedBy, EndReason: r.EndReason,
			StartedAt: fromNanos(r.StartedAt), EndedAt: fromNanos(r.EndedAt),
		}
	}
	return out, nil
}

func (s *Store) ThreadByAgentSession(ctx context.Context, cli, agentSessionID string) (Thread, bool, error) {
	r, err := s.q.GetPAgentThreadByAgentSession(ctx, gen.GetPAgentThreadByAgentSessionParams{Cli: cli, AgentSessionID: agentSessionID})
	return one(r, err, threadFrom)
}

func (s *Store) ThreadBySession(ctx context.Context, sessionID string) (Thread, bool, error) {
	r, err := s.q.GetPAgentThreadBySession(ctx, sessionID)
	return one(r, err, threadFrom)
}

// InsertEvent stores e, setting CreatedAt, and returns its ID.
func (s *Store) InsertEvent(ctx context.Context, e Event) (int64, error) {
	payload := string(e.Payload)
	if payload == "" {
		payload = "null"
	}
	return s.q.InsertPAgentThreadEvent(ctx, gen.InsertPAgentThreadEventParams{
		ThreadID: e.ThreadID, Kind: e.Kind, StartedBy: e.StartedBy, Payload: payload,
		AnchorSession: e.AnchorSession, AnchorIndex: int64(e.AnchorIndex), CreatedAt: s.now().UnixNano(),
	})
}

// Events returns the thread's events with an ID above afterID, oldest first.
func (s *Store) Events(ctx context.Context, threadID string, afterID int64) ([]Event, error) {
	rows, err := s.q.ListPAgentThreadEvents(ctx, gen.ListPAgentThreadEventsParams{ThreadID: threadID, ID: afterID})
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(rows))
	for i, r := range rows {
		out[i] = Event{
			ID: r.ID, ThreadID: r.ThreadID, Kind: r.Kind, StartedBy: r.StartedBy, Payload: json.RawMessage(r.Payload),
			AnchorSession: r.AnchorSession, AnchorIndex: int(r.AnchorIndex), CreatedAt: fromNanos(r.CreatedAt),
		}
	}
	return out, nil
}
