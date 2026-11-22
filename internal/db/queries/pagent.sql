-- name: InsertPAgent :exec
INSERT INTO pagent (id, name, dir, archived_at, created_at) VALUES (?, ?, ?, 0, ?);

-- name: GetPAgentByID :one
SELECT id, name, dir, archived_at, created_at FROM pagent WHERE id = ?;

-- name: GetPAgentByName :one
SELECT id, name, dir, archived_at, created_at FROM pagent WHERE name = ? AND archived_at = 0;

-- name: GetPAgentByDir :one
SELECT id, name, dir, archived_at, created_at FROM pagent WHERE dir = ?;

-- name: ListActivePAgents :many
SELECT id, name, dir, archived_at, created_at FROM pagent
WHERE archived_at = 0
ORDER BY created_at, id;

-- name: ListAllPAgents :many
SELECT id, name, dir, archived_at, created_at FROM pagent
ORDER BY created_at, id;

-- name: RenamePAgent :execrows
UPDATE pagent SET name = ? WHERE id = ?;

-- name: SetPAgentArchivedAt :execrows
UPDATE pagent SET archived_at = ? WHERE id = ?;

-- name: DeletePAgent :exec
DELETE FROM pagent WHERE id = ?;

-- name: InsertPAgentThread :exec
INSERT INTO pagent_thread (id, agent_id, kind, started_by, title, status, read_position, created_at, last_active_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?);

-- name: LatestPAgentThread :one
SELECT id, agent_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM pagent_thread
WHERE agent_id = ? AND kind = ? AND status = 'active'
ORDER BY last_active_at DESC, rowid DESC
LIMIT 1;

-- name: ListPAgentThreads :many
SELECT id, agent_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM pagent_thread WHERE agent_id = ?
ORDER BY last_active_at DESC, rowid DESC;

-- name: GetPAgentThread :one
SELECT id, agent_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM pagent_thread WHERE id = ?;

-- name: TouchPAgentThread :execrows
UPDATE pagent_thread SET last_active_at = ? WHERE id = ?;

-- name: SetPAgentThreadReadPosition :execrows
UPDATE pagent_thread SET read_position = ? WHERE id = ?;

-- name: SetPAgentThreadStatus :execrows
UPDATE pagent_thread SET status = ? WHERE id = ?;

-- name: InsertPAgentThreadSession :exec
INSERT INTO pagent_thread_session (thread_id, session_id, cli, agent_session_id, transcript_path, started_by, started_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: SetPAgentSessionAgentID :execrows
UPDATE pagent_thread_session
SET cli = ?, agent_session_id = ?, transcript_path = ?
WHERE session_id = ? AND ended_at = 0;

-- name: ReleasePAgentAgentSession :exec
UPDATE pagent_thread_session SET agent_session_id = ''
WHERE cli = ? AND agent_session_id = ? AND agent_session_id != '' AND session_id != ?;

-- name: EndPAgentThreadSession :execrows
UPDATE pagent_thread_session SET ended_at = ?, end_reason = ? WHERE session_id = ? AND ended_at = 0;

-- name: SetPAgentSessionEndReason :execrows
UPDATE pagent_thread_session
SET end_reason = sqlc.arg(end_reason),
    ended_at = CASE WHEN ended_at = 0 THEN sqlc.arg(ended_at) ELSE ended_at END
WHERE thread_id = sqlc.arg(thread_id) AND session_id = sqlc.arg(session_id);

-- name: ListPAgentThreadSessions :many
SELECT thread_id, session_id, cli, agent_session_id, transcript_path, started_by, started_at, ended_at, end_reason
FROM pagent_thread_session WHERE thread_id = ?
ORDER BY started_at, rowid;

-- name: GetPAgentThreadByAgentSession :one
SELECT id, agent_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM pagent_thread WHERE id = (
    SELECT thread_id FROM pagent_thread_session
    WHERE cli = ? AND agent_session_id = ? AND agent_session_id != ''
);

-- name: GetPAgentThreadBySession :one
SELECT id, agent_id, kind, started_by, title, status, read_position, created_at, last_active_at
FROM pagent_thread WHERE id = (
    SELECT thread_id FROM pagent_thread_session
    WHERE session_id = ?
    ORDER BY started_at DESC, rowid DESC
    LIMIT 1
);

-- name: InsertPAgentThreadEvent :one
INSERT INTO pagent_thread_event (thread_id, kind, started_by, payload, anchor_session, anchor_index, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListPAgentThreadEvents :many
SELECT id, thread_id, kind, started_by, payload, anchor_session, anchor_index, created_at
FROM pagent_thread_event WHERE thread_id = ? AND id > ?
ORDER BY id;

-- name: DeletePAgentEvents :exec
DELETE FROM pagent_thread_event WHERE thread_id IN (SELECT id FROM pagent_thread WHERE agent_id = ?);

-- name: DeletePAgentCursors :exec
DELETE FROM pagent_capture_cursor WHERE thread_id IN (SELECT id FROM pagent_thread WHERE agent_id = ?);

-- name: DeletePAgentSessions :exec
DELETE FROM pagent_thread_session WHERE thread_id IN (SELECT id FROM pagent_thread WHERE agent_id = ?);

-- name: DeletePAgentProposals :exec
DELETE FROM pagent_memory_proposal WHERE agent_id = ?;

-- name: DeletePAgentThreads :exec
DELETE FROM pagent_thread WHERE agent_id = ?;

-- name: InsertPAgentProposal :exec
INSERT INTO pagent_memory_proposal (id, agent_id, thread_id, ops, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetPAgentProposal :one
SELECT id, agent_id, thread_id, ops, created_at, decided_at, decision
FROM pagent_memory_proposal WHERE id = ?;

-- name: ListPendingPAgentProposals :many
SELECT id, agent_id, thread_id, ops, created_at, decided_at, decision
FROM pagent_memory_proposal WHERE agent_id = ? AND decided_at = 0
ORDER BY created_at, id;

-- name: DecidePAgentProposal :execrows
UPDATE pagent_memory_proposal SET decided_at = ?, decision = ?
WHERE id = ? AND decided_at = 0;
