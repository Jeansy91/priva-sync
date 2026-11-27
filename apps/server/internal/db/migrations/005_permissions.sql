-- +goose Up
ALTER TABLE files ADD COLUMN permissions JSONB;

-- +goose Down
ALTER TABLE files DROP COLUMN permissions;
