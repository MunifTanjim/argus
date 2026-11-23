-- name: UpsertBotRoutineState :exec
INSERT INTO bot_routine_state (
    bot_id, slug, def_hash, enabled, next_due_at, last_run_at, last_result, consecutive_failures,
    paused_reason, sessions_used, cost_used, tokens_used, check_back_at, anchor_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (bot_id, slug) DO UPDATE SET
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

-- name: ListBotRoutineStates :many
SELECT * FROM bot_routine_state WHERE bot_id = ? ORDER BY slug;

-- name: AdvanceBotRoutineNextDue :exec
UPDATE bot_routine_state SET next_due_at = ?, updated_at = ? WHERE bot_id = ? AND slug = ?;

-- name: SetBotRoutineEnabled :exec
UPDATE bot_routine_state SET enabled = ?, paused_reason = ?, updated_at = ? WHERE bot_id = ? AND slug = ?;

-- name: SetBotRoutineDef :exec
UPDATE bot_routine_state SET def_hash = ?, next_due_at = ?, updated_at = ? WHERE bot_id = ? AND slug = ?;

-- name: DeleteBotRoutineState :exec
DELETE FROM bot_routine_state WHERE bot_id = ? AND slug = ?;

-- name: DeleteBotRoutineStates :exec
DELETE FROM bot_routine_state WHERE bot_id = ?;

-- name: InsertBotRoutineRun :execrows
INSERT INTO bot_routine_run (id, bot_id, slug, occurrence, started_by, test, state, lease_until, started_at, retry_of)
VALUES (?, ?, ?, ?, ?, ?, 'claimed', ?, ?, ?)
ON CONFLICT (bot_id, slug, occurrence) DO NOTHING;

-- name: GetBotRoutineRun :one
SELECT * FROM bot_routine_run WHERE id = ?;

-- name: SetBotRoutineRunState :execrows
UPDATE bot_routine_run SET state = ?, error = ?, ended_at = ? WHERE id = ? AND state IN ('claimed', 'running', 'queued');

-- name: SetBotRoutineRunThread :execrows
UPDATE bot_routine_run SET thread_id = ?, state = 'running', lease_until = ? WHERE id = ? AND state IN ('claimed', 'running', 'queued');

-- name: RenewBotRoutineRunLease :execrows
UPDATE bot_routine_run SET lease_until = ? WHERE id = ? AND state IN ('claimed', 'running');

-- name: ExpiredBotRoutineRuns :many
SELECT * FROM bot_routine_run
WHERE state IN ('claimed', 'running') AND lease_until > 0 AND lease_until < ?
ORDER BY started_at, id;

-- name: BotRoutineRunsSince :many
SELECT * FROM bot_routine_run
WHERE bot_id = ? AND slug = ? AND started_at >= ?
ORDER BY started_at, id;

-- name: ActiveBotRoutineRuns :many
SELECT * FROM bot_routine_run
WHERE bot_id = ? AND slug = ? AND state IN ('claimed', 'running', 'queued')
ORDER BY started_at, id;

-- name: LastStartedBotRoutineRun :one
SELECT * FROM bot_routine_run
WHERE bot_id = ? AND slug = ? AND test = 0 AND state NOT IN ('skipped', 'missed', 'queued')
ORDER BY started_at DESC, id DESC
LIMIT 1;

-- name: RecordBotRoutineRunResult :execrows
UPDATE bot_routine_run
SET result = ?, summary = ?, actions_done = ?, actions_pending = ?, unverified = ?, sources = ?, cost = ?, tokens = ?
WHERE id = ?;

-- name: CountBotRoutineRunsSince :one
SELECT COUNT(*) FROM bot_routine_run
WHERE bot_id = ? AND started_at >= ? AND test = 0 AND state NOT IN ('skipped', 'missed');

-- name: DeleteBotRoutineRuns :exec
DELETE FROM bot_routine_run WHERE bot_id = ?;

-- name: UpsertBotWebhookSecret :exec
INSERT INTO bot_webhook_secret (bot_id, slug, secret_hash, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT (bot_id, slug) DO UPDATE SET secret_hash = excluded.secret_hash, created_at = excluded.created_at;

-- name: GetBotWebhookSecretHash :one
SELECT secret_hash FROM bot_webhook_secret WHERE bot_id = ? AND slug = ?;

-- name: DeleteBotWebhookSecrets :exec
DELETE FROM bot_webhook_secret WHERE bot_id = ?;

-- name: InterruptBotRoutineRun :execrows
UPDATE bot_routine_run SET state = 'interrupted', error = sqlc.arg(error), ended_at = sqlc.arg(ended_at)
WHERE id = sqlc.arg(id) AND state IN ('claimed', 'running') AND result = '' AND lease_until < sqlc.arg(now);

-- name: RecordBotRoutineOutcome :one
UPDATE bot_routine_state
SET last_run_at = sqlc.arg(last_run_at), last_result = sqlc.arg(last_result),
    consecutive_failures = (consecutive_failures + 1) * sqlc.arg(failed),
    sessions_used = sessions_used + sqlc.arg(sessions),
    cost_used = cost_used + sqlc.arg(cost),
    tokens_used = tokens_used + sqlc.arg(tokens),
    updated_at = sqlc.arg(updated_at)
WHERE bot_id = sqlc.arg(bot_id) AND slug = sqlc.arg(slug)
RETURNING *;

-- name: SetBotRoutineCheckBack :exec
UPDATE bot_routine_state SET check_back_at = sqlc.arg(check_back_at), updated_at = sqlc.arg(updated_at)
WHERE bot_id = sqlc.arg(bot_id) AND slug = sqlc.arg(slug);

-- name: BotRoutineRunsByBotSince :many
SELECT * FROM bot_routine_run
WHERE bot_id = sqlc.arg(bot_id) AND started_at >= sqlc.arg(started_at)
ORDER BY started_at, id;
