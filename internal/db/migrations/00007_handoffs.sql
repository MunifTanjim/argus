-- +goose Up
-- +goose StatementBegin

CREATE TABLE pagent_handoff (
    id          TEXT PRIMARY KEY,
    root_id     TEXT NOT NULL,
    depth       INTEGER NOT NULL,
    ancestors   TEXT NOT NULL DEFAULT '[]',
    from_node   TEXT NOT NULL DEFAULT '',
    from_agent  TEXT NOT NULL,
    from_name   TEXT NOT NULL DEFAULT '',
    from_thread TEXT NOT NULL DEFAULT '',
    from_session TEXT NOT NULL DEFAULT '',
    to_node     TEXT NOT NULL DEFAULT '',
    to_agent    TEXT NOT NULL,
    to_name     TEXT NOT NULL DEFAULT '',
    to_thread   TEXT NOT NULL DEFAULT '',
    request     TEXT NOT NULL DEFAULT '{}',
    state       TEXT NOT NULL,
    answer      TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    answered_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX pagent_handoff_idx_root ON pagent_handoff (root_id);
CREATE INDEX pagent_handoff_idx_to_thread ON pagent_handoff (to_thread);
CREATE INDEX pagent_handoff_idx_state ON pagent_handoff (state, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE pagent_handoff;

-- +goose StatementEnd
