-- name: InsertOrchMessage :exec
INSERT INTO orch_message (
    id, kind, from_node, from_key, from_session_id, from_label,
    to_node, to_key, body,
    reply_to, child_node, child_key, interaction, state, answered_by_node, answered_by_key,
    request_id, request_hash, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ReceiveOrchMessage :execrows
INSERT INTO orch_message (
    id, kind, from_node, from_key, from_session_id, from_label,
    to_node, to_key, body,
    reply_to, child_node, child_key, interaction, state, answered_by_node, answered_by_key,
    request_id, request_hash, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)
ON CONFLICT (id) DO NOTHING;

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

-- name: FailQueuedOrchMessages :many
UPDATE orch_message
SET state = 'failed', updated_at = sqlc.arg(updated_at)
WHERE to_node = sqlc.arg(to_node) AND to_key = sqlc.arg(to_key)
    AND state = 'queued' AND from_key != 'argus'
RETURNING *;

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

-- name: RekeyOrchOutboxSender :exec
UPDATE orch_peer_outbox SET params = json_set(params, '$.message.from.key', sqlc.arg(new_key))
WHERE method = 'orch.peer.deliver'
    AND json_extract(params, '$.message.from.node') = sqlc.arg(node)
    AND json_extract(params, '$.message.from.key') = sqlc.arg(old_key);

-- name: RekeyOrchOutboxChild :exec
UPDATE orch_peer_outbox SET params = json_set(params, '$.message.child.key', sqlc.arg(new_key))
WHERE method = 'orch.peer.deliver'
    AND json_extract(params, '$.message.child.node') = sqlc.arg(node)
    AND json_extract(params, '$.message.child.key') = sqlc.arg(old_key);

-- name: RekeyOrchOutboxRecipient :exec
UPDATE orch_peer_outbox SET params = json_set(params, '$.message.to.key', sqlc.arg(new_key))
WHERE node = sqlc.arg(node) AND method = 'orch.peer.deliver'
    AND json_extract(params, '$.message.to.node') = sqlc.arg(node)
    AND json_extract(params, '$.message.to.key') = sqlc.arg(old_key);

-- name: ExpireOrchMessages :execrows
DELETE FROM orch_message WHERE created_at < ?;

-- name: PutOrchLink :exec
INSERT INTO orch_link (
    child_key, child_node, parent_node, parent_key, task_id, ended_by,
    request_id, request_hash, child_session_id, parent_session_id, parent_ended, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (child_node, child_key) DO UPDATE SET
    parent_node = excluded.parent_node,
    parent_key = excluded.parent_key,
    task_id = excluded.task_id,
    ended_by = excluded.ended_by,
    request_id = excluded.request_id,
    request_hash = excluded.request_hash,
    child_session_id = excluded.child_session_id,
    parent_session_id = excluded.parent_session_id,
    parent_ended = excluded.parent_ended;

-- name: GetOrchLink :one
SELECT * FROM orch_link WHERE child_node = ? AND child_key = ?;

-- name: GetOrchLinkByRequest :one
SELECT * FROM orch_link WHERE parent_node = ? AND parent_key = ? AND request_id = ?;

-- name: ListOrchLinks :many
SELECT * FROM orch_link ORDER BY created_at, child_node, child_key;

-- name: SetOrchLinkEndedBy :exec
UPDATE orch_link SET ended_by = ? WHERE child_node = ? AND child_key = ?;

-- name: SetOrchLinksParentEnded :exec
UPDATE orch_link SET parent_ended = 1 WHERE parent_node = ? AND parent_key = ?;

-- name: DeleteOrchLink :exec
DELETE FROM orch_link WHERE child_node = ? AND child_key = ?;

-- name: InsertOrchOutbox :exec
INSERT INTO orch_peer_outbox (node, method, params, created_at) VALUES (?, ?, ?, ?);

-- name: PendingOrchOutbox :many
SELECT * FROM orch_peer_outbox WHERE node = ? AND parked = 0 ORDER BY seq LIMIT ?;

-- name: FailOrchOutbox :one
UPDATE orch_peer_outbox
SET attempts = attempts + 1,
    last_attempt_at = sqlc.arg(now),
    parked = CASE WHEN attempts + 1 >= sqlc.arg(park_after) THEN 1 ELSE 0 END
WHERE seq = sqlc.arg(seq) AND (attempts = 0 OR sqlc.arg(now) - last_attempt_at >= sqlc.arg(min_gap))
RETURNING parked;

-- name: DeleteOrchOutbox :exec
DELETE FROM orch_peer_outbox WHERE seq = ?;

-- name: DropOrchOutboxDeliver :execrows
DELETE FROM orch_peer_outbox
WHERE node = sqlc.arg(node) AND method = 'orch.peer.deliver'
    AND json_extract(params, '$.message.id') = sqlc.arg(message_id);

-- name: OrchOutboxNodes :many
SELECT DISTINCT node FROM orch_peer_outbox WHERE parked = 0 ORDER BY node;

-- name: ExpireOrchOutbox :execrows
DELETE FROM orch_peer_outbox WHERE created_at < ?;

-- name: OrchMessagePeerNodes :many
SELECT DISTINCT CAST(CASE WHEN to_node = sqlc.arg(node) AND to_key = sqlc.arg(key) THEN from_node ELSE to_node END AS TEXT) AS peer
FROM orch_message
WHERE (to_node = sqlc.arg(node) AND to_key = sqlc.arg(key)) OR (from_node = sqlc.arg(node) AND from_key = sqlc.arg(key));
