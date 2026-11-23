-- name: InsertBot :exec
INSERT INTO bot (id, name, dir, archived_at, created_at) VALUES (?, ?, ?, 0, ?);

-- name: GetBotByID :one
SELECT id, name, dir, archived_at, created_at FROM bot WHERE id = ?;

-- name: GetBotByName :one
SELECT id, name, dir, archived_at, created_at FROM bot WHERE name = ? AND archived_at = 0;

-- name: GetBotByDir :one
SELECT id, name, dir, archived_at, created_at FROM bot WHERE dir = ?;

-- name: ListActiveBots :many
SELECT id, name, dir, archived_at, created_at FROM bot
WHERE archived_at = 0
ORDER BY created_at, id;

-- name: ListAllBots :many
SELECT id, name, dir, archived_at, created_at FROM bot
ORDER BY created_at, id;

-- name: RenameBot :execrows
UPDATE bot SET name = ? WHERE id = ?;

-- name: SetBotArchivedAt :execrows
UPDATE bot SET archived_at = ? WHERE id = ?;

-- name: DeleteBot :exec
DELETE FROM bot WHERE id = ?;

-- name: InsertBotThread :exec
INSERT INTO bot_thread (id, bot_id, kind, started_by, title, status, read_position, created_at, last_active_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?);

-- name: LatestBotThread :one
SELECT id, bot_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM bot_thread
WHERE bot_id = ? AND kind = ? AND status = 'active'
ORDER BY last_active_at DESC, rowid DESC
LIMIT 1;

-- name: ListBotThreads :many
SELECT id, bot_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM bot_thread WHERE bot_id = ?
ORDER BY last_active_at DESC, rowid DESC;

-- name: GetBotThread :one
SELECT id, bot_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM bot_thread WHERE id = ?;

-- name: TouchBotThread :execrows
UPDATE bot_thread SET last_active_at = ? WHERE id = ?;

-- name: SetBotThreadReadPosition :execrows
UPDATE bot_thread SET read_position = ? WHERE id = ?;

-- name: SetBotThreadStatus :execrows
UPDATE bot_thread SET status = ? WHERE id = ?;

-- name: InsertBotThreadSession :exec
INSERT INTO bot_thread_session (thread_id, session_id, cli, agent_session_id, transcript_path, started_by, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: SetBotSessionAgentID :execrows
UPDATE bot_thread_session
SET cli = ?, agent_session_id = ?, transcript_path = ?
WHERE session_id = ? AND ended_at = 0;

-- name: ReleaseBotAgentSession :exec
UPDATE bot_thread_session SET agent_session_id = ''
WHERE cli = ? AND agent_session_id = ? AND agent_session_id != '' AND session_id != ?;

-- name: EndBotThreadSession :execrows
UPDATE bot_thread_session SET ended_at = ?, end_reason = ? WHERE session_id = ? AND ended_at = 0;

-- name: SetBotSessionEndReason :execrows
UPDATE bot_thread_session
SET end_reason = sqlc.arg(end_reason),
    ended_at = CASE WHEN ended_at = 0 THEN sqlc.arg(ended_at) ELSE ended_at END
WHERE thread_id = sqlc.arg(thread_id) AND session_id = sqlc.arg(session_id);

-- name: ListBotThreadSessions :many
SELECT thread_id, session_id, cli, agent_session_id, transcript_path, started_by, started_at, ended_at, end_reason
FROM bot_thread_session WHERE thread_id = ?
ORDER BY started_at, rowid;

-- name: GetBotThreadByAgentSession :one
SELECT id, bot_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM bot_thread WHERE id = (
    SELECT thread_id FROM bot_thread_session
    WHERE cli = ? AND agent_session_id = ? AND agent_session_id != ''
);

-- name: GetBotThreadBySession :one
SELECT id, bot_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM bot_thread WHERE id = (
    SELECT thread_id FROM bot_thread_session
    WHERE session_id = ?
    ORDER BY started_at DESC, rowid DESC
    LIMIT 1
);

-- name: InsertBotThreadEvent :one
INSERT INTO bot_thread_event (thread_id, kind, started_by, payload, anchor_session, anchor_index, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListBotThreadEvents :many
SELECT id, thread_id, kind, started_by, payload, anchor_session, anchor_index, created_at
FROM bot_thread_event WHERE thread_id = ? AND id > ?
ORDER BY id;

-- name: DeleteBotEvents :exec
DELETE FROM bot_thread_event WHERE thread_id IN (SELECT id FROM bot_thread WHERE bot_id = ?);

-- name: GetBotCaptureCursor :one
SELECT thread_id, session_id, entry_count, last_entry_id, updated_at
FROM bot_capture_cursor WHERE thread_id = ? AND session_id = ?;

-- name: UpsertBotCaptureCursor :exec
INSERT INTO bot_capture_cursor (thread_id, session_id, entry_count, last_entry_id, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (thread_id, session_id) DO UPDATE SET
    entry_count = excluded.entry_count,
    last_entry_id = excluded.last_entry_id,
    updated_at = excluded.updated_at;

-- name: DeleteBotCursors :exec
DELETE FROM bot_capture_cursor WHERE thread_id IN (SELECT id FROM bot_thread WHERE bot_id = ?);

-- name: DeleteBotSessions :exec
DELETE FROM bot_thread_session WHERE thread_id IN (SELECT id FROM bot_thread WHERE bot_id = ?);

-- name: DeleteBotProposals :exec
DELETE FROM bot_memory_proposal WHERE bot_id = ?;

-- name: DeleteBotThreads :exec
DELETE FROM bot_thread WHERE bot_id = ?;

-- name: InsertBotProposal :exec
INSERT INTO bot_memory_proposal (id, bot_id, thread_id, ops, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetBotProposal :one
SELECT id, bot_id, thread_id, ops, created_at, decided_at, decision
FROM bot_memory_proposal WHERE id = ?;

-- name: ListPendingBotProposals :many
SELECT id, bot_id, thread_id, ops, created_at, decided_at, decision
FROM bot_memory_proposal WHERE bot_id = ? AND decided_at = 0
ORDER BY created_at, id;

-- name: DecideBotProposal :execrows
UPDATE bot_memory_proposal SET decided_at = ?, decision = ?
WHERE id = ? AND decided_at = 0;
