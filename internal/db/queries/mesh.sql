-- name: InsertMeshMessage :exec
INSERT INTO mesh_message (
    id, kind, sender_node, sender_key, recipient_node, recipient_key, body,
    reply_to, subject_key, interaction, state, answered_by, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMeshMessage :one
SELECT * FROM mesh_message WHERE id = ?;

-- name: NextQueuedMeshMessage :one
SELECT * FROM mesh_message
WHERE recipient_key = ? AND state = 'queued'
ORDER BY created_at, id
LIMIT 1;

-- name: QueuedMeshMessages :many
SELECT * FROM mesh_message
WHERE recipient_key = ? AND state = 'queued'
ORDER BY created_at, id;

-- name: QueuedMeshRecipients :many
SELECT DISTINCT recipient_key FROM mesh_message WHERE state = 'queued';

-- name: SetMeshMessageState :exec
UPDATE mesh_message
SET state = sqlc.arg(state), answered_by = sqlc.arg(answered_by), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: TransitionMeshMessageState :execrows
UPDATE mesh_message
SET state = sqlc.arg(to_state), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: ClaimMeshNotice :execrows
UPDATE mesh_message
SET state = 'replied', answered_by = sqlc.arg(answered_by), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND kind = 'prompt_notice' AND state = sqlc.arg(from_state);

-- name: ReleaseMeshNotice :execrows
UPDATE mesh_message
SET state = sqlc.arg(to_state), answered_by = '', updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'replied' AND answered_by = sqlc.arg(answered_by);

-- name: FailQueuedMeshMessages :execrows
UPDATE mesh_message
SET state = 'failed', updated_at = sqlc.arg(updated_at)
WHERE recipient_key = sqlc.arg(recipient_key) AND state = 'queued';

-- name: OpenMeshNotices :many
SELECT * FROM mesh_message
WHERE subject_key = ? AND kind = 'prompt_notice' AND state IN ('queued', 'delivered')
ORDER BY created_at, id;

-- name: UnrepliedMeshTask :one
SELECT * FROM mesh_message
WHERE recipient_key = ? AND kind = 'task' AND state = 'delivered'
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ListMeshMessages :many
SELECT * FROM mesh_message ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: RekeyMeshSender :exec
UPDATE mesh_message SET sender_key = sqlc.arg(new_key) WHERE sender_key = sqlc.arg(old_key);

-- name: RekeyMeshRecipient :exec
UPDATE mesh_message SET recipient_key = sqlc.arg(new_key) WHERE recipient_key = sqlc.arg(old_key);

-- name: RekeyMeshSubject :exec
UPDATE mesh_message SET subject_key = sqlc.arg(new_key) WHERE subject_key = sqlc.arg(old_key);

-- name: RekeyMeshLinkChild :exec
UPDATE mesh_link SET child_key = sqlc.arg(new_key) WHERE child_key = sqlc.arg(old_key);

-- name: RekeyMeshLinkParent :exec
UPDATE mesh_link SET parent_key = sqlc.arg(new_key) WHERE parent_key = sqlc.arg(old_key);

-- name: ExpireMeshMessages :execrows
DELETE FROM mesh_message WHERE created_at < ?;

-- name: PutMeshLink :exec
INSERT INTO mesh_link (child_key, child_node, parent_node, parent_key, task_id, ended_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (child_key) DO UPDATE SET
    child_node = excluded.child_node,
    parent_node = excluded.parent_node,
    parent_key = excluded.parent_key,
    task_id = excluded.task_id,
    ended_by = excluded.ended_by;

-- name: GetMeshLink :one
SELECT * FROM mesh_link WHERE child_key = ?;

-- name: ListMeshLinks :many
SELECT * FROM mesh_link ORDER BY created_at, child_key;

-- name: SetMeshLinkEndedBy :exec
UPDATE mesh_link SET ended_by = ? WHERE child_key = ?;

-- name: DeleteMeshLink :exec
DELETE FROM mesh_link WHERE child_key = ?;
