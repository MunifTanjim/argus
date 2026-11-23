-- +goose Up
-- +goose StatementBegin

CREATE TABLE bot_activity (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    bot_id   TEXT NOT NULL,
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

CREATE INDEX bot_activity_idx_bot_id ON bot_activity (bot_id, id);

CREATE TABLE bot_secret (
    bot_id    TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    value_enc   BLOB NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (bot_id, name)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE bot_secret;
DROP TABLE bot_activity;

-- +goose StatementEnd
