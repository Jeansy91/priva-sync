-- +goose Up

-- Empty-directory sync: files table now also holds directory entries
-- (is_dir = TRUE, size = 0, content_hash = NULL). Path uniqueness is
-- shared with file rows, so a path cannot be both a file and a dir.
ALTER TABLE files
    ADD COLUMN IF NOT EXISTS is_dir BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_files_is_dir
    ON files(sync_root_id, is_dir)
    WHERE is_dir = TRUE;

-- +goose Down

DROP INDEX IF EXISTS idx_files_is_dir;
ALTER TABLE files DROP COLUMN IF EXISTS is_dir;
