-- name: InsertPAgentHandoff :exec
INSERT INTO pagent_handoff (
    id, root_id, depth, ancestors, from_node, from_agent, from_name, from_thread, from_session,
    to_node, to_agent, to_name, to_thread, request, state, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetPAgentHandoff :one
SELECT * FROM pagent_handoff WHERE id = ?;

-- name: PAgentHandoffByToThread :one
SELECT * FROM pagent_handoff WHERE to_thread = ? AND to_thread != '' LIMIT 1;

-- name: CountPAgentHandoffsByRoot :one
SELECT COUNT(*) FROM pagent_handoff WHERE root_id = ?;

-- name: SetPAgentHandoffToThread :exec
UPDATE pagent_handoff SET to_thread = ? WHERE id = ?;

-- name: EndPAgentHandoff :execrows
UPDATE pagent_handoff SET state = ?, answer = ?, answered_at = ? WHERE id = ? AND state = 'pending';

-- name: PendingPAgentHandoffsBefore :many
SELECT * FROM pagent_handoff WHERE state = 'pending' AND created_at < ? AND from_node = '' ORDER BY created_at;
