-- +goose Up
-- +goose StatementBegin

CREATE TABLE pagent_activity (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id   TEXT NOT NULL,
    thread_id  TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    started_by TEXT NOT NULL DEFAULT '',
    tool       TEXT NOT NULL DEFAULT '',
    target     TEXT NOT NULL DEFAULT '',
    class      TEXT NOT NULL DEFAULT '',
    decision   TEXT NOT NULL,
    decided_by TEXT NOT NULL,
    rule       TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    hook       TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE INDEX pagent_activity_idx_agent_id ON pagent_activity (agent_id, id);

CREATE TABLE pagent_secret (
    agent_id    TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    value_enc   BLOB NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (agent_id, name)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE pagent_secret;
DROP TABLE pagent_activity;

-- +goose StatementEnd
