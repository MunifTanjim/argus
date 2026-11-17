-- +goose Up
-- +goose StatementBegin

CREATE TABLE orch_message (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL,
    from_node        TEXT NOT NULL,
    from_key         TEXT NOT NULL,
    from_session_id  TEXT NOT NULL DEFAULT '',
    from_label       TEXT NOT NULL DEFAULT '',
    to_node          TEXT NOT NULL,
    to_key           TEXT NOT NULL,
    body             TEXT NOT NULL,
    reply_to         TEXT NOT NULL DEFAULT '',
    child_node       TEXT NOT NULL DEFAULT '',
    child_key        TEXT NOT NULL DEFAULT '',
    interaction      TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL,
    answered_by_node TEXT NOT NULL DEFAULT '',
    answered_by_key  TEXT NOT NULL DEFAULT '',
    request_id       TEXT NOT NULL DEFAULT '',
    request_hash     TEXT NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE INDEX orch_message_idx_to ON orch_message (to_node, to_key, state);
CREATE INDEX orch_message_idx_child ON orch_message (child_key, kind, state);
CREATE UNIQUE INDEX orch_message_idx_request ON orch_message (from_node, from_key, request_id) WHERE request_id != '';

CREATE TABLE orch_link (
    child_node   TEXT NOT NULL,
    child_key    TEXT NOT NULL,
    parent_node  TEXT NOT NULL,
    parent_key   TEXT NOT NULL,
    task_id      TEXT NOT NULL DEFAULT '',
    ended_by     TEXT NOT NULL DEFAULT '',
    request_id   TEXT NOT NULL DEFAULT '',
    request_hash TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    child_session_id  TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT NOT NULL DEFAULT '',
    parent_ended      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (child_node, child_key)
);

CREATE UNIQUE INDEX orch_link_idx_request ON orch_link (parent_node, parent_key, request_id) WHERE request_id != '';

CREATE TABLE orch_peer_outbox (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    node       TEXT NOT NULL,
    method     TEXT NOT NULL,
    params     TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    parked     INTEGER NOT NULL DEFAULT 0,
    last_attempt_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX orch_peer_outbox_idx_node ON orch_peer_outbox (node, seq);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX orch_peer_outbox_idx_node;
DROP TABLE orch_peer_outbox;
DROP INDEX orch_link_idx_request;
DROP TABLE orch_link;
DROP INDEX orch_message_idx_request;
DROP INDEX orch_message_idx_child;
DROP INDEX orch_message_idx_to;
DROP TABLE orch_message;

-- +goose StatementEnd
