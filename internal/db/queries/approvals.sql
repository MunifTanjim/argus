-- name: InsertPAgentActivity :one
INSERT INTO pagent_activity (agent_id, thread_id, session_id, started_by, tool, target, class, decision, decided_by, rule, reason, hook, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListPAgentActivity :many
SELECT id, agent_id, thread_id, session_id, started_by, tool, target, class, decision, decided_by, rule, reason, hook, created_at
FROM pagent_activity
WHERE agent_id = sqlc.arg(agent_id) AND (CAST(sqlc.arg(unattended) AS INTEGER) = 0 OR started_by != 'you')
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: DeletePAgentActivity :exec
DELETE FROM pagent_activity WHERE agent_id = ?;
