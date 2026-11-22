-- +goose Up
-- +goose StatementBegin

CREATE TABLE pagent_routine_state (
    agent_id             TEXT NOT NULL,
    slug                 TEXT NOT NULL,
    def_hash             TEXT NOT NULL DEFAULT '',
    enabled              INTEGER NOT NULL DEFAULT 1,
    next_due_at          INTEGER NOT NULL DEFAULT 0,
    last_run_at          INTEGER NOT NULL DEFAULT 0,
    last_result          TEXT NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    paused_reason        TEXT NOT NULL DEFAULT '',
    sessions_used        INTEGER NOT NULL DEFAULT 0,
    cost_used            REAL NOT NULL DEFAULT 0,
    tokens_used          INTEGER NOT NULL DEFAULT 0,
    check_back_at        INTEGER NOT NULL DEFAULT 0,
    anchor_at            INTEGER NOT NULL DEFAULT 0,
    updated_at           INTEGER NOT NULL,
    PRIMARY KEY (agent_id, slug)
);

CREATE TABLE pagent_routine_run (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL,
    slug            TEXT NOT NULL,
    occurrence      INTEGER NOT NULL,
    started_by      TEXT NOT NULL,
    test            INTEGER NOT NULL DEFAULT 0,
    state           TEXT NOT NULL,
    lease_until     INTEGER NOT NULL DEFAULT 0,
    thread_id       TEXT NOT NULL DEFAULT '',
    result          TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    actions_done    TEXT NOT NULL DEFAULT '[]',
    actions_pending TEXT NOT NULL DEFAULT '[]',
    unverified      TEXT NOT NULL DEFAULT '[]',
    sources         TEXT NOT NULL DEFAULT '[]',
    cost            REAL NOT NULL DEFAULT 0,
    tokens          INTEGER NOT NULL DEFAULT 0,
    started_at      INTEGER NOT NULL,
    ended_at        INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX pagent_routine_run_idx_occurrence ON pagent_routine_run (agent_id, slug, occurrence);
CREATE INDEX pagent_routine_run_idx_agent_started ON pagent_routine_run (agent_id, started_at);
CREATE INDEX pagent_routine_run_idx_state_lease ON pagent_routine_run (state, lease_until);

CREATE TABLE pagent_webhook_secret (
    agent_id    TEXT NOT NULL,
    slug        TEXT NOT NULL,
    secret_hash TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (agent_id, slug)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE pagent_webhook_secret;
DROP TABLE pagent_routine_run;
DROP TABLE pagent_routine_state;

-- +goose StatementEnd
