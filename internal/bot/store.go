package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MunifTanjim/argus/internal/db/gen"
)

// Store persists bots, their threads, sessions and events.
type Store struct {
	db  *sql.DB
	q   *gen.Queries
	now func() time.Time
}

func NewStore(db *sql.DB, now func() time.Time) *Store {
	return &Store{db: db, q: gen.New(db), now: now}
}

type Bot struct {
	ID, Name, Dir         string
	ArchivedAt, CreatedAt time.Time
}

type Thread struct {
	ID, BotID, Kind, StartedBy, Title, Status string
	ReadPosition                              int64
	CreatedAt, LastActiveAt                   time.Time
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
var ErrNotFound = errors.New("bot: not found")

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

func botFrom(r gen.Bot) Bot {
	return Bot{ID: r.ID, Name: r.Name, Dir: r.Dir, ArchivedAt: fromNanos(r.ArchivedAt), CreatedAt: fromNanos(r.CreatedAt)}
}

func threadFrom(r gen.BotThread) Thread {
	return Thread{
		ID: r.ID, BotID: r.BotID, Kind: r.Kind, StartedBy: r.StartedBy, Title: r.Title, Status: r.Status,
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

// InsertBot stores a, setting CreatedAt.
func (s *Store) InsertBot(ctx context.Context, a Bot) error {
	return s.q.InsertBot(ctx, gen.InsertBotParams{ID: a.ID, Name: a.Name, Dir: a.Dir, CreatedAt: s.now().UnixNano()})
}

// BotByName finds an active bot.
func (s *Store) BotByName(ctx context.Context, name string) (Bot, bool, error) {
	r, err := s.q.GetBotByName(ctx, name)
	return one(r, err, botFrom)
}

func (s *Store) BotByID(ctx context.Context, id string) (Bot, bool, error) {
	r, err := s.q.GetBotByID(ctx, id)
	return one(r, err, botFrom)
}

func (s *Store) BotByDir(ctx context.Context, dir string) (Bot, bool, error) {
	r, err := s.q.GetBotByDir(ctx, dir)
	return one(r, err, botFrom)
}

func (s *Store) ListBots(ctx context.Context, includeArchived bool) ([]Bot, error) {
	list := s.q.ListActiveBots
	if includeArchived {
		list = s.q.ListAllBots
	}
	rows, err := list(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Bot, len(rows))
	for i, r := range rows {
		out[i] = botFrom(r)
	}
	return out, nil
}

func (s *Store) RenameBot(ctx context.Context, id, name string) error {
	return affected(s.q.RenameBot(ctx, gen.RenameBotParams{Name: name, ID: id}))
}

func (s *Store) ArchiveBot(ctx context.Context, id string) error {
	return affected(s.q.SetBotArchivedAt(ctx, gen.SetBotArchivedAtParams{ArchivedAt: s.now().UnixNano(), ID: id}))
}

// RestoreBot fails when an active bot already has the name.
func (s *Store) RestoreBot(ctx context.Context, id string) error {
	a, ok, err := s.BotByID(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := affected(s.q.SetBotArchivedAt(ctx, gen.SetBotArchivedAtParams{ArchivedAt: 0, ID: id})); err != nil {
		if _, taken, _ := s.BotByName(ctx, a.Name); taken {
			return fmt.Errorf("bot name %s is taken", a.Name)
		}
		return err
	}
	return nil
}

// PurgeBot deletes the bot and everything stored for it.
func (s *Store) PurgeBot(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("bot: purge: %w", err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	for _, del := range []func(context.Context, string) error{
		q.DeleteBotEvents, q.DeleteBotCursors, q.DeleteBotSessions,
		q.DeleteBotProposals, q.DeleteBotRoutineRuns, q.DeleteBotRoutineStates,
		q.DeleteBotWebhookSecrets, q.DeleteBotActivity, q.DeleteBotSecrets, q.DeleteBotThreads, q.DeleteBot,
	} {
		if err := del(ctx, id); err != nil {
			return fmt.Errorf("bot: purge: %w", err)
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
	return s.q.InsertBotThread(ctx, gen.InsertBotThreadParams{
		ID: t.ID, BotID: t.BotID, Kind: t.Kind, StartedBy: t.StartedBy, Title: t.Title, Status: t.Status,
		CreatedAt: now, LastActiveAt: now,
	})
}

func (s *Store) Thread(ctx context.Context, id string) (Thread, bool, error) {
	r, err := s.q.GetBotThread(ctx, id)
	return one(r, err, threadFrom)
}

// LatestThread returns the active thread of kind most recently active.
func (s *Store) LatestThread(ctx context.Context, botID, kind string) (Thread, bool, error) {
	r, err := s.q.LatestBotThread(ctx, gen.LatestBotThreadParams{BotID: botID, Kind: kind})
	return one(r, err, threadFrom)
}

// ListThreads returns all threads of the bot, most recently active first.
func (s *Store) ListThreads(ctx context.Context, botID string) ([]Thread, error) {
	rows, err := s.q.ListBotThreads(ctx, botID)
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
	return affected(s.q.TouchBotThread(ctx, gen.TouchBotThreadParams{LastActiveAt: s.now().UnixNano(), ID: id}))
}

func (s *Store) SetReadPosition(ctx context.Context, id string, pos int64) error {
	return affected(s.q.SetBotThreadReadPosition(ctx, gen.SetBotThreadReadPositionParams{ReadPosition: pos, ID: id}))
}

func (s *Store) CloseThread(ctx context.Context, id string) error {
	return affected(s.q.SetBotThreadStatus(ctx, gen.SetBotThreadStatusParams{Status: ThreadClosed, ID: id}))
}

// InsertThreadSession stores ts, setting StartedAt. An agent session id
// belongs to one thread session: a row that held ts.AgentSessionID gives it up.
func (s *Store) InsertThreadSession(ctx context.Context, ts ThreadSession) error {
	return s.inTx(ctx, func(q *gen.Queries) error {
		if err := q.ReleaseBotAgentSession(ctx, gen.ReleaseBotAgentSessionParams{
			Cli: ts.CLI, AgentSessionID: ts.AgentSessionID, SessionID: ts.SessionID,
		}); err != nil {
			return err
		}
		return q.InsertBotThreadSession(ctx, gen.InsertBotThreadSessionParams{
			ThreadID: ts.ThreadID, SessionID: ts.SessionID, Cli: ts.CLI, AgentSessionID: ts.AgentSessionID,
			TranscriptPath: ts.TranscriptPath, StartedBy: ts.StartedBy, StartedAt: s.now().UnixNano(),
		})
	})
}

// SetAgentSessionID records the agent session of the session's open thread
// session, taking the id from any row that held it.
func (s *Store) SetAgentSessionID(ctx context.Context, sessionID, cli, agentSessionID, transcriptPath string) error {
	return s.inTx(ctx, func(q *gen.Queries) error {
		if err := q.ReleaseBotAgentSession(ctx, gen.ReleaseBotAgentSessionParams{
			Cli: cli, AgentSessionID: agentSessionID, SessionID: sessionID,
		}); err != nil {
			return err
		}
		return affected(q.SetBotSessionAgentID(ctx, gen.SetBotSessionAgentIDParams{
			Cli: cli, AgentSessionID: agentSessionID, TranscriptPath: transcriptPath, SessionID: sessionID,
		}))
	})
}

// SetEndReason sets the end reason of a thread session, ending it if open.
func (s *Store) SetEndReason(ctx context.Context, threadID, sessionID, reason string) error {
	return affected(s.q.SetBotSessionEndReason(ctx, gen.SetBotSessionEndReasonParams{
		EndReason: reason, EndedAt: s.now().UnixNano(), ThreadID: threadID, SessionID: sessionID,
	}))
}

func (s *Store) inTx(ctx context.Context, fn func(*gen.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// EndThreadSession records that the session ended; an already ended session is left as is.
func (s *Store) EndThreadSession(ctx context.Context, sessionID, reason string) error {
	n, err := s.q.EndBotThreadSession(ctx, gen.EndBotThreadSessionParams{
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
	rows, err := s.q.ListBotThreadSessions(ctx, threadID)
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
	r, err := s.q.GetBotThreadByAgentSession(ctx, gen.GetBotThreadByAgentSessionParams{Cli: cli, AgentSessionID: agentSessionID})
	return one(r, err, threadFrom)
}

func (s *Store) ThreadBySession(ctx context.Context, sessionID string) (Thread, bool, error) {
	r, err := s.q.GetBotThreadBySession(ctx, sessionID)
	return one(r, err, threadFrom)
}

// InsertEvent stores e, setting CreatedAt, and returns its ID.
func (s *Store) InsertEvent(ctx context.Context, e Event) (int64, error) {
	payload := string(e.Payload)
	if payload == "" {
		payload = "null"
	}
	return s.q.InsertBotThreadEvent(ctx, gen.InsertBotThreadEventParams{
		ThreadID: e.ThreadID, Kind: e.Kind, StartedBy: e.StartedBy, Payload: payload,
		AnchorSession: e.AnchorSession, AnchorIndex: int64(e.AnchorIndex), CreatedAt: s.now().UnixNano(),
	})
}

// Events returns the thread's events with an ID above afterID, oldest first.
func (s *Store) Events(ctx context.Context, threadID string, afterID int64) ([]Event, error) {
	rows, err := s.q.ListBotThreadEvents(ctx, gen.ListBotThreadEventsParams{ThreadID: threadID, ID: afterID})
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

// Proposal is a staged memory change awaiting review. Ops is opaque JSON
// owned by the caller. Decision is empty while pending.
type Proposal struct {
	ID, BotID, ThreadID  string
	Ops                  json.RawMessage
	Decision             string
	CreatedAt, DecidedAt time.Time
}

func proposalFrom(r gen.BotMemoryProposal) Proposal {
	return Proposal{
		ID: r.ID, BotID: r.BotID, ThreadID: r.ThreadID, Ops: json.RawMessage(r.Ops), Decision: r.Decision,
		CreatedAt: fromNanos(r.CreatedAt), DecidedAt: fromNanos(r.DecidedAt),
	}
}

// InsertProposal stores p as pending, setting CreatedAt.
func (s *Store) InsertProposal(ctx context.Context, p Proposal) error {
	return s.q.InsertBotProposal(ctx, gen.InsertBotProposalParams{
		ID: p.ID, BotID: p.BotID, ThreadID: p.ThreadID, Ops: string(p.Ops), CreatedAt: s.now().UnixNano(),
	})
}

func (s *Store) Proposal(ctx context.Context, id string) (Proposal, bool, error) {
	r, err := s.q.GetBotProposal(ctx, id)
	return one(r, err, proposalFrom)
}

// PendingProposals returns the bot's undecided proposals, oldest first.
func (s *Store) PendingProposals(ctx context.Context, botID string) ([]Proposal, error) {
	rows, err := s.q.ListPendingBotProposals(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := make([]Proposal, len(rows))
	for i, r := range rows {
		out[i] = proposalFrom(r)
	}
	return out, nil
}

// DecideProposal records decision on a pending proposal; ErrNotFound when it
// does not exist or was already decided.
func (s *Store) DecideProposal(ctx context.Context, id, decision string) error {
	return affected(s.q.DecideBotProposal(ctx, gen.DecideBotProposalParams{
		DecidedAt: s.now().UnixNano(), Decision: decision, ID: id,
	}))
}

// CaptureCursor marks how much of a thread session's transcript memory
// capture has read: the first EntryCount entries, the last having LastEntryID.
type CaptureCursor struct {
	ThreadID, SessionID, LastEntryID string
	EntryCount                       int
	UpdatedAt                        time.Time
}

func (s *Store) CaptureCursor(ctx context.Context, threadID, sessionID string) (CaptureCursor, bool, error) {
	r, err := s.q.GetBotCaptureCursor(ctx, gen.GetBotCaptureCursorParams{ThreadID: threadID, SessionID: sessionID})
	return one(r, err, func(r gen.BotCaptureCursor) CaptureCursor {
		return CaptureCursor{
			ThreadID: r.ThreadID, SessionID: r.SessionID, LastEntryID: r.LastEntryID,
			EntryCount: int(r.EntryCount), UpdatedAt: fromNanos(r.UpdatedAt),
		}
	})
}

// SetCaptureCursor stores c; a zero UpdatedAt means now.
func (s *Store) SetCaptureCursor(ctx context.Context, c CaptureCursor) error {
	at := c.UpdatedAt
	if at.IsZero() {
		at = s.now()
	}
	return s.q.UpsertBotCaptureCursor(ctx, gen.UpsertBotCaptureCursorParams{
		ThreadID: c.ThreadID, SessionID: c.SessionID, EntryCount: int64(c.EntryCount),
		LastEntryID: c.LastEntryID, UpdatedAt: at.UnixNano(),
	})
}
