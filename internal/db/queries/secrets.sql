-- name: UpsertPAgentSecret :exec
INSERT INTO pagent_secret (agent_id, name, description, value_enc, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (agent_id, name) DO UPDATE SET
    description = excluded.description,
    value_enc = excluded.value_enc,
    updated_at = excluded.updated_at;

-- name: ListPAgentSecrets :many
SELECT agent_id, name, description, value_enc, updated_at
FROM pagent_secret WHERE agent_id = ?
ORDER BY name;

-- name: DeletePAgentSecret :execrows
DELETE FROM pagent_secret WHERE agent_id = ? AND name = ?;

-- name: DeletePAgentSecrets :exec
DELETE FROM pagent_secret WHERE agent_id = ?;
