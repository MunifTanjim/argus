-- name: InsertBotActivity :one
INSERT INTO bot_activity (bot_id, thread_id, session_id, started_by, tool, target, class, decision, decided_by, rule, reason, hook, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListBotActivity :many
SELECT id, bot_id, thread_id, session_id, started_by, tool, target, class, decision, decided_by, rule, reason, hook, created_at
FROM bot_activity
WHERE bot_id = sqlc.arg(bot_id) AND (CAST(sqlc.arg(unattended) AS INTEGER) = 0 OR started_by != 'you')
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: DeleteBotActivity :exec
DELETE FROM bot_activity WHERE bot_id = ?;
