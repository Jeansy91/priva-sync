-- +goose Up

-- Physical trash location on disk for recovering soft-deleted files.
-- NULL = file was never deleted / permanently removed without trash.
ALTER TABLE files ADD COLUMN trash_path TEXT;

-- When the file was moved to trash (for retention-based purge).
ALTER TABLE files ADD COLUMN deleted_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE files DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE files DROP COLUMN IF EXISTS trash_path;
