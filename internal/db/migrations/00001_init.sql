-- +goose Up
-- +goose StatementBegin

CREATE TABLE project (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('git', 'plain')),
    dir          TEXT NOT NULL UNIQUE,
    is_gone      BOOLEAN NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    hidden       BOOLEAN NOT NULL DEFAULT 0,
    pinned       BOOLEAN NOT NULL DEFAULT 0
);

CREATE TABLE workspace (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES project (id) ON DELETE CASCADE,
    dir          TEXT NOT NULL UNIQUE,
    is_main      BOOLEAN NOT NULL DEFAULT 0,
    is_gone      BOOLEAN NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX workspace_idx_project_id ON workspace (project_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX workspace_idx_project_id;
DROP TABLE workspace;
DROP TABLE project;

-- +goose StatementEnd
