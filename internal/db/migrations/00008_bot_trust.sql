-- +goose Up
-- +goose StatementBegin

-- bot_trust holds, per bot repository, the content hashes of the files that
-- Argus checks are unchanged before it commits there.
CREATE TABLE bot_trust (
    repo       TEXT PRIMARY KEY,
    items      TEXT NOT NULL DEFAULT '{}',
    updated_at INTEGER NOT NULL
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE bot_trust;

-- +goose StatementEnd
