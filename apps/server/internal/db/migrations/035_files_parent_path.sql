-- +goose Up

-- ADR-0041: a directory is a row, not an inference.
--
-- Until now the directory tree was derived from file paths: listing a level
-- reduced every row in the subtree to its first segment. That cost tracks the
-- size of the subtree, not the size of the page, so the top level of a
-- million-file root took ~1.9s no matter how few entries were asked for. A
-- level whose members are rows costs one index range instead.
--
-- Two consequences that are not implementation detail:
--   * A directory that loses its last file no longer disappears. It stays,
--     the way it does in a filesystem.
--   * Every materialised directory gets a seq and therefore reaches the
--     clients through the changes feed once, right after this migration.

ALTER TABLE files ADD COLUMN parent_path TEXT;

-- parent_path is the containing directory including its trailing separator,
-- so parent_path || <name> reconstructs path exactly. Root-level rows carry
-- "/".
--
-- The backfill runs with the seq trigger off. That trigger assigns a fresh
-- seq on every UPDATE -- correct for a real change, wrong here: touching a
-- million rows would renumber the whole feed and hand every client a full
-- resync of files that did not change.
ALTER TABLE files DISABLE TRIGGER files_assign_seq;

UPDATE files
SET parent_path = left(path, length(path) - length(split_part(path, '/', -1)));

ALTER TABLE files ENABLE TRIGGER files_assign_seq;

-- Derived by the database, never written by the application: path and
-- parent_path cannot drift, including through the bulk UPDATE that moves a
-- whole subtree.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION derive_files_parent_path()
RETURNS TRIGGER AS $$
BEGIN
    NEW.parent_path = left(NEW.path, length(NEW.path) - length(split_part(NEW.path, '/', -1)));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER files_derive_parent_path
    BEFORE INSERT OR UPDATE ON files
    FOR EACH ROW
    EXECUTE FUNCTION derive_files_parent_path();

ALTER TABLE files ALTER COLUMN parent_path SET NOT NULL;

-- Materialise the directories that only ever existed as a prefix of some
-- file's path. These inserts do want a seq: from a client's point of view a
-- directory it never heard of has appeared.
--
-- A live non-directory row already sitting at an ancestor path is left alone.
-- That state predates this migration and is contradictory either way; the
-- repair sweep reports it rather than overwriting a file with a directory.
WITH RECURSIVE ancestors AS (
    SELECT user_id, sync_root_id,
           left(path, length(path) - length(split_part(path, '/', -1)) - 1) AS path
    FROM files
    WHERE is_deleted = FALSE
    UNION
    SELECT user_id, sync_root_id,
           left(path, length(path) - length(split_part(path, '/', -1)) - 1)
    FROM ancestors
    WHERE path <> ''
)
INSERT INTO files (user_id, sync_root_id, path, size, mtime, is_dir, is_deleted)
SELECT DISTINCT ON (a.sync_root_id, a.path) a.user_id, a.sync_root_id, a.path, 0, NOW(), TRUE, FALSE
FROM ancestors a
WHERE a.path <> ''
  AND NOT EXISTS (
      SELECT 1 FROM files f
      WHERE f.sync_root_id = a.sync_root_id AND f.path = a.path AND f.is_deleted = FALSE
  );

-- The whole point: one level is one index range.
CREATE INDEX IF NOT EXISTS idx_files_level
    ON files (sync_root_id, parent_path, path)
    WHERE is_deleted = FALSE;

-- +goose Down

DROP INDEX IF EXISTS idx_files_level;
DROP TRIGGER IF EXISTS files_derive_parent_path ON files;
DROP FUNCTION IF EXISTS derive_files_parent_path;
ALTER TABLE files DROP COLUMN IF EXISTS parent_path;
