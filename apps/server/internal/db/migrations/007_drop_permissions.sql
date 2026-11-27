-- +goose Up
ALTER TABLE files DROP COLUMN IF EXISTS permissions;

-- +goose Down
ALTER TABLE files ADD COLUMN permissions JSONB;
