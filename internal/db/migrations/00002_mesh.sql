-- +goose Up
-- +goose StatementBegin

CREATE TABLE mesh_message (
    id             TEXT PRIMARY KEY,
    kind           TEXT NOT NULL,
    sender_node    TEXT NOT NULL,
    sender_key     TEXT NOT NULL,
    recipient_node TEXT NOT NULL,
    recipient_key  TEXT NOT NULL,
    body           TEXT NOT NULL,
    reply_to       TEXT NOT NULL DEFAULT '',
    subject_key    TEXT NOT NULL DEFAULT '',
    interaction    TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL,
    answered_by    TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE INDEX mesh_message_idx_recipient ON mesh_message (recipient_key, state, created_at);
CREATE INDEX mesh_message_idx_subject ON mesh_message (subject_key, kind, state);

CREATE TABLE mesh_link (
    child_key   TEXT PRIMARY KEY,
    child_node  TEXT NOT NULL,
    parent_node TEXT NOT NULL,
    parent_key  TEXT NOT NULL,
    task_id     TEXT NOT NULL DEFAULT '',
    ended_by    TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE INDEX mesh_link_idx_parent ON mesh_link (parent_key);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX mesh_link_idx_parent;
DROP TABLE mesh_link;
DROP INDEX mesh_message_idx_subject;
DROP INDEX mesh_message_idx_recipient;
DROP TABLE mesh_message;

-- +goose StatementEnd
