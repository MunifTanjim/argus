-- +goose Up
-- +goose StatementBegin

CREATE TABLE bot (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    dir         TEXT NOT NULL,
    archived_at INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE UNIQUE INDEX bot_idx_name ON bot (name) WHERE archived_at = 0;
CREATE UNIQUE INDEX bot_idx_dir ON bot (dir);

CREATE TABLE bot_thread (
    id             TEXT PRIMARY KEY,
    bot_id       TEXT NOT NULL,
    kind           TEXT NOT NULL,
    started_by     TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL,
    read_position  INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    last_active_at INTEGER NOT NULL
);

CREATE INDEX bot_thread_idx_bot_kind_last_active ON bot_thread (bot_id, kind, last_active_at);

CREATE TABLE bot_thread_session (
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

CREATE UNIQUE INDEX bot_thread_session_idx_agent_session ON bot_thread_session (cli, agent_session_id) WHERE agent_session_id != '';
CREATE INDEX bot_thread_session_idx_thread_started ON bot_thread_session (thread_id, started_at);
CREATE INDEX bot_thread_session_idx_session ON bot_thread_session (session_id);

CREATE TABLE bot_thread_event (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    thread_id      TEXT NOT NULL,
    kind           TEXT NOT NULL,
    started_by     TEXT NOT NULL,
    payload        TEXT NOT NULL,
    anchor_session TEXT NOT NULL DEFAULT '',
    anchor_index   INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL
);

CREATE INDEX bot_thread_event_idx_thread_id ON bot_thread_event (thread_id, id);

CREATE TABLE bot_capture_cursor (
    thread_id     TEXT NOT NULL,
    session_id    TEXT NOT NULL,
    entry_count   INTEGER NOT NULL,
    last_entry_id TEXT NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (thread_id, session_id)
);

CREATE TABLE bot_memory_proposal (
    id         TEXT PRIMARY KEY,
    bot_id   TEXT NOT NULL,
    thread_id  TEXT NOT NULL,
    ops        TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    decided_at INTEGER NOT NULL DEFAULT 0,
    decision   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX bot_memory_proposal_idx_bot ON bot_memory_proposal (bot_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE bot_memory_proposal;
DROP TABLE bot_capture_cursor;
DROP TABLE bot_thread_event;
DROP TABLE bot_thread_session;
DROP TABLE bot_thread;
DROP TABLE bot;

-- +goose StatementEnd
