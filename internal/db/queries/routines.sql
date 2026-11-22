-- name: UpsertPAgentRoutineState :exec
INSERT INTO pagent_routine_state (
    agent_id, slug, def_hash, enabled, next_due_at, last_run_at, last_result, consecutive_failures,
    paused_reason, sessions_used, cost_used, tokens_used, check_back_at, anchor_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (agent_id, slug) DO UPDATE SET
    def_hash = excluded.def_hash,
    enabled = excluded.enabled,
    next_due_at = excluded.next_due_at,
    last_run_at = excluded.last_run_at,
    last_result = excluded.last_result,
    consecutive_failures = excluded.consecutive_failures,
    paused_reason = excluded.paused_reason,
    sessions_used = excluded.sessions_used,
    cost_used = excluded.cost_used,
    tokens_used = excluded.tokens_used,
    check_back_at = excluded.check_back_at,
    anchor_at = excluded.anchor_at,
    updated_at = excluded.updated_at;

-- name: ListPAgentRoutineStates :many
SELECT * FROM pagent_routine_state WHERE agent_id = ? ORDER BY slug;

-- name: AdvancePAgentRoutineNextDue :exec
UPDATE pagent_routine_state SET next_due_at = ?, updated_at = ? WHERE agent_id = ? AND slug = ?;

-- name: SetPAgentRoutineEnabled :exec
UPDATE pagent_routine_state SET enabled = ?, paused_reason = ?, updated_at = ? WHERE agent_id = ? AND slug = ?;

-- name: SetPAgentRoutineDef :exec
UPDATE pagent_routine_state SET def_hash = ?, next_due_at = ?, updated_at = ? WHERE agent_id = ? AND slug = ?;

-- name: DeletePAgentRoutineState :exec
DELETE FROM pagent_routine_state WHERE agent_id = ? AND slug = ?;

-- name: DeletePAgentRoutineStates :exec
DELETE FROM pagent_routine_state WHERE agent_id = ?;

-- name: InsertPAgentRoutineRun :execrows
INSERT INTO pagent_routine_run (id, agent_id, slug, occurrence, started_by, test, state, lease_until, started_at, retry_of)
VALUES (?, ?, ?, ?, ?, ?, 'claimed', ?, ?, ?)
ON CONFLICT (agent_id, slug, occurrence) DO NOTHING;

-- name: GetPAgentRoutineRun :one
SELECT * FROM pagent_routine_run WHERE id = ?;

-- name: SetPAgentRoutineRunState :execrows
UPDATE pagent_routine_run SET state = ?, error = ?, ended_at = ? WHERE id = ? AND state IN ('claimed', 'running', 'queued');

-- name: SetPAgentRoutineRunThread :execrows
UPDATE pagent_routine_run SET thread_id = ?, state = 'running', lease_until = ? WHERE id = ? AND state IN ('claimed', 'running', 'queued');

-- name: RenewPAgentRoutineRunLease :execrows
UPDATE pagent_routine_run SET lease_until = ? WHERE id = ? AND state IN ('claimed', 'running');

-- name: ExpiredPAgentRoutineRuns :many
SELECT * FROM pagent_routine_run
WHERE state IN ('claimed', 'running') AND lease_until > 0 AND lease_until < ? AND result = ''
ORDER BY started_at, id;

-- name: PAgentRoutineRunsSince :many
SELECT * FROM pagent_routine_run
WHERE agent_id = ? AND slug = ? AND started_at >= ?
ORDER BY started_at, id;

-- name: ActivePAgentRoutineRuns :many
SELECT * FROM pagent_routine_run
WHERE agent_id = ? AND slug = ? AND state IN ('claimed', 'running', 'queued')
ORDER BY started_at, id;

-- name: LastStartedPAgentRoutineRun :one
SELECT * FROM pagent_routine_run
WHERE agent_id = ? AND slug = ? AND test = 0 AND state NOT IN ('skipped', 'missed', 'queued')
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: RecordPAgentRoutineRunResult :execrows
UPDATE pagent_routine_run
SET result = ?, summary = ?, actions_done = ?, actions_pending = ?, unverified = ?, sources = ?, cost = ?, tokens = ?
WHERE id = ?;

-- name: CountPAgentRoutineRunsSince :one
SELECT COUNT(*) FROM pagent_routine_run
WHERE agent_id = ? AND started_at >= ? AND test = 0 AND state NOT IN ('skipped', 'missed');

-- name: DeletePAgentRoutineRuns :exec
DELETE FROM pagent_routine_run WHERE agent_id = ?;

-- name: UpsertPAgentWebhookSecret :exec
INSERT INTO pagent_webhook_secret (agent_id, slug, secret_hash, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT (agent_id, slug) DO UPDATE SET secret_hash = excluded.secret_hash, created_at = excluded.created_at;

-- name: GetPAgentWebhookSecretHash :one
SELECT secret_hash FROM pagent_webhook_secret WHERE agent_id = ? AND slug = ?;

-- name: DeletePAgentWebhookSecrets :exec
DELETE FROM pagent_webhook_secret WHERE agent_id = ?;

-- name: InterruptPAgentRoutineRun :execrows
UPDATE pagent_routine_run SET state = 'interrupted', error = sqlc.arg(error), ended_at = sqlc.arg(ended_at)
WHERE id = sqlc.arg(id) AND state IN ('claimed', 'running') AND result = '' AND lease_until < sqlc.arg(now);

-- name: RecordPAgentRoutineOutcome :one
UPDATE pagent_routine_state
SET last_run_at = sqlc.arg(last_run_at), last_result = sqlc.arg(last_result),
    consecutive_failures = (consecutive_failures + 1) * sqlc.arg(failed),
    sessions_used = sessions_used + sqlc.arg(sessions),
    cost_used = cost_used + sqlc.arg(cost),
    tokens_used = tokens_used + sqlc.arg(tokens),
    updated_at = sqlc.arg(updated_at)
WHERE agent_id = sqlc.arg(agent_id) AND slug = sqlc.arg(slug)
RETURNING *;
