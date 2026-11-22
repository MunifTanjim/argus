-- +goose Up
-- +goose StatementBegin

CREATE TABLE pagent (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    dir         TEXT NOT NULL,
    archived_at INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE UNIQUE INDEX pagent_idx_name ON pagent (name) WHERE archived_at = 0;
CREATE UNIQUE INDEX pagent_idx_dir ON pagent (dir);

CREATE TABLE pagent_thread (
    id             TEXT PRIMARY KEY,
    agent_id       TEXT NOT NULL,
    kind           TEXT NOT NULL,
    started_by     TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL,
    read_position  INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    last_active_at INTEGER NOT NULL
);

CREATE INDEX pagent_thread_idx_agent_kind_last_active ON pagent_thread (agent_id, kind, last_active_at);

CREATE TABLE pagent_thread_session (
    thread_id        TEXT NOT NULL,
    session_id       TEXT NOT NULL,
    cli              TEXT NOT NULL,
    agent_session_id TEXT NOT NULL DEFAULT '',
    transcript_path  TEXT NOT NULL DEFAULT '',
    started_by       TEXT NOT NULL,
    started_at       INTEGER NOT NULL,
    ended_at         INTEGER NOT NULL DEFAULT 0,
    end_reason       TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX pagent_thread_session_idx_agent_session ON pagent_thread_session (cli, agent_session_id) WHERE agent_session_id != '';
CREATE INDEX pagent_thread_session_idx_thread_started ON pagent_thread_session (thread_id, started_at);
CREATE INDEX pagent_thread_session_idx_session ON pagent_thread_session (session_id);

CREATE TABLE pagent_thread_event (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    thread_id      TEXT NOT NULL,
    kind           TEXT NOT NULL,
    started_by     TEXT NOT NULL,
    payload        TEXT NOT NULL,
    anchor_session TEXT NOT NULL DEFAULT '',
    anchor_index   INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL
);

CREATE INDEX pagent_thread_event_idx_thread_id ON pagent_thread_event (thread_id, id);

CREATE TABLE pagent_capture_cursor (
    thread_id     TEXT NOT NULL,
    session_id    TEXT NOT NULL,
    entry_count   INTEGER NOT NULL,
    last_entry_id TEXT NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (thread_id, session_id)
);

CREATE TABLE pagent_memory_proposal (
    id         TEXT PRIMARY KEY,
    agent_id   TEXT NOT NULL,
    thread_id  TEXT NOT NULL,
    ops        TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    decided_at INTEGER NOT NULL DEFAULT 0,
    decision   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX pagent_memory_proposal_idx_agent ON pagent_memory_proposal (agent_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE pagent_memory_proposal;
DROP TABLE pagent_capture_cursor;
DROP TABLE pagent_thread_event;
DROP TABLE pagent_thread_session;
DROP TABLE pagent_thread;
DROP TABLE pagent;

-- +goose StatementEnd
