-- +goose Up
-- +goose StatementBegin

CREATE TABLE bot_handoff (
    id          TEXT PRIMARY KEY,
    root_id     TEXT NOT NULL,
    depth       INTEGER NOT NULL,
    ancestors   TEXT NOT NULL DEFAULT '[]',
    from_node   TEXT NOT NULL DEFAULT '',
    from_bot  TEXT NOT NULL,
    from_name   TEXT NOT NULL DEFAULT '',
    from_thread TEXT NOT NULL DEFAULT '',
    from_session TEXT NOT NULL DEFAULT '',
    to_node     TEXT NOT NULL DEFAULT '',
    to_bot    TEXT NOT NULL,
    to_name     TEXT NOT NULL DEFAULT '',
    to_thread   TEXT NOT NULL DEFAULT '',
    request     TEXT NOT NULL DEFAULT '{}',
    state       TEXT NOT NULL,
    answer      TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    answered_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX bot_handoff_idx_root ON bot_handoff (root_id);
CREATE INDEX bot_handoff_idx_to_thread ON bot_handoff (to_thread);
CREATE INDEX bot_handoff_idx_state ON bot_handoff (state, created_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE bot_handoff;

-- +goose StatementEnd
