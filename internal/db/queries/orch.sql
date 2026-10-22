-- name: InsertOrchMessage :exec
INSERT INTO orch_message (
    id, kind, from_node, from_key,
    to_node, to_key, body,
    reply_to, child_node, child_key, interaction, state, answered_by_node, answered_by_key,
    request_id, request_hash, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetOrchMessage :one
SELECT * FROM orch_message WHERE id = ?;

-- name: GetOrchMessageByRequest :one
SELECT * FROM orch_message WHERE from_node = ? AND from_key = ? AND request_id = ?;

-- name: QueuedOrchMessages :many
SELECT * FROM orch_message
WHERE to_node = ? AND to_key = ? AND state = 'queued'
ORDER BY rowid;

-- name: QueuedOrchTo :many
SELECT DISTINCT to_key FROM orch_message WHERE to_node = ? AND state = 'queued';

-- name: SetOrchMessageState :exec
UPDATE orch_message
SET state = sqlc.arg(state), answered_by_node = sqlc.arg(answered_by_node), answered_by_key = sqlc.arg(answered_by_key), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: TransitionOrchMessageState :execrows
UPDATE orch_message
SET state = sqlc.arg(to_state), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: ClaimOrchInteraction :execrows
UPDATE orch_message
SET state = 'replied', answered_by_node = sqlc.arg(answered_by_node), answered_by_key = sqlc.arg(answered_by_key), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND kind = 'interaction' AND state = sqlc.arg(from_state);

-- name: ReleaseOrchInteraction :execrows
UPDATE orch_message
SET state = sqlc.arg(to_state), answered_by_node = '', answered_by_key = '', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'replied'
    AND answered_by_node = sqlc.arg(answered_by_node) AND answered_by_key = sqlc.arg(answered_by_key);

-- name: FailQueuedOrchMessages :execrows
UPDATE orch_message
SET state = 'failed', updated_at = sqlc.arg(updated_at)
WHERE to_node = sqlc.arg(to_node) AND to_key = sqlc.arg(to_key)
    AND state = 'queued' AND from_key != 'argus';

-- name: OpenOrchInteractions :many
SELECT * FROM orch_message
WHERE child_node = ? AND child_key = ? AND kind = 'interaction' AND state IN ('queued', 'delivered')
ORDER BY created_at, id;

-- name: UnrepliedOrchTask :one
SELECT * FROM orch_message
WHERE to_node = ? AND to_key = ? AND kind = 'task' AND state = 'delivered'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ListOrchMessages :many
SELECT * FROM orch_message ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: RekeyOrchNodeSender :exec
UPDATE orch_message SET from_key = sqlc.arg(new_key)
WHERE from_node = sqlc.arg(node) AND from_key = sqlc.arg(old_key);

-- name: RekeyOrchNodeRecipient :exec
UPDATE orch_message SET to_key = sqlc.arg(new_key)
WHERE to_node = sqlc.arg(node) AND to_key = sqlc.arg(old_key);

-- name: RekeyOrchNodeChild :exec
UPDATE orch_message SET child_key = sqlc.arg(new_key)
WHERE child_node = sqlc.arg(node) AND child_key = sqlc.arg(old_key);

-- name: RekeyOrchNodeLinkChild :exec
UPDATE orch_link SET child_key = sqlc.arg(new_key)
WHERE child_node = sqlc.arg(node) AND child_key = sqlc.arg(old_key);

-- name: RekeyOrchNodeLinkParent :exec
UPDATE orch_link SET parent_key = sqlc.arg(new_key)
WHERE parent_node = sqlc.arg(node) AND parent_key = sqlc.arg(old_key);

-- name: ExpireOrchMessages :execrows
DELETE FROM orch_message WHERE created_at < ?;

-- name: PutOrchLink :exec
INSERT INTO orch_link (
    child_key, child_node, parent_node, parent_key, task_id, ended_by,
    request_id, request_hash, child_session_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (child_node, child_key) DO UPDATE SET
    parent_node = excluded.parent_node,
    parent_key = excluded.parent_key,
    task_id = excluded.task_id,
    ended_by = excluded.ended_by,
    request_id = excluded.request_id,
    request_hash = excluded.request_hash,
    child_session_id = excluded.child_session_id;

-- name: GetOrchLink :one
SELECT * FROM orch_link WHERE child_node = ? AND child_key = ?;

-- name: GetOrchLinkByRequest :one
SELECT * FROM orch_link WHERE parent_node = ? AND parent_key = ? AND request_id = ?;

-- name: ListOrchLinks :many
SELECT * FROM orch_link ORDER BY created_at, child_node, child_key;

-- name: SetOrchLinkEndedBy :exec
UPDATE orch_link SET ended_by = ? WHERE child_node = ? AND child_key = ?;

-- name: DeleteOrchLink :exec
DELETE FROM orch_link WHERE child_node = ? AND child_key = ?;
