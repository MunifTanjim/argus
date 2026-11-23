-- name: UpsertBotSecret :exec
INSERT INTO bot_secret (bot_id, name, description, value_enc, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (bot_id, name) DO UPDATE SET
    description = excluded.description,
    value_enc = excluded.value_enc,
    updated_at = excluded.updated_at;

-- name: ListBotSecrets :many
SELECT bot_id, name, description, value_enc, updated_at
FROM bot_secret WHERE bot_id = ?
ORDER BY name;

-- name: DeleteBotSecret :execrows
DELETE FROM bot_secret WHERE bot_id = ? AND name = ?;

-- name: DeleteBotSecrets :exec
DELETE FROM bot_secret WHERE bot_id = ?;
