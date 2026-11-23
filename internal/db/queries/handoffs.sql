-- name: InsertBotHandoff :exec
INSERT INTO bot_handoff (
    id, root_id, depth, ancestors, from_node, from_bot, from_name, from_thread, from_session,
    to_node, to_bot, to_name, to_thread, request, state, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetBotHandoff :one
SELECT * FROM bot_handoff WHERE id = ?;

-- name: BotHandoffByToThread :one
SELECT * FROM bot_handoff WHERE to_thread = ? AND to_thread != '' LIMIT 1;

-- name: CountBotHandoffsByRoot :one
SELECT COUNT(*) FROM bot_handoff WHERE root_id = ?;

-- name: SetBotHandoffToThread :exec
UPDATE bot_handoff SET to_thread = ? WHERE id = ?;

-- name: EndBotHandoff :execrows
UPDATE bot_handoff SET state = ?, answer = ?, answered_at = ? WHERE id = ? AND state = 'pending';

-- name: PendingBotHandoffsBefore :many
SELECT * FROM bot_handoff WHERE state = 'pending' AND created_at < ? AND from_node = '' ORDER BY created_at;
