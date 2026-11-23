-- +goose Up
-- +goose StatementBegin

ALTER TABLE bot_routine_run ADD COLUMN retry_of TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE bot_routine_run DROP COLUMN retry_of;

-- +goose StatementEnd
