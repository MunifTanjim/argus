-- name: GetBotTrust :one
SELECT items FROM bot_trust WHERE repo = ?;

-- name: UpsertBotTrust :exec
INSERT INTO bot_trust (repo, items, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (repo) DO UPDATE SET
    items = excluded.items,
    updated_at = excluded.updated_at;

-- name: DeleteBotTrust :exec
DELETE FROM bot_trust WHERE repo = ?;
