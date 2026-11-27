-- +goose Up

-- Trigram index for server-side file/directory search on million-file
-- roots (see ADR-0008). The partial index saves space and matches
-- exactly what search may return: non-deleted entries.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_files_path_trgm
    ON files USING gin (lower(path) gin_trgm_ops)
    WHERE is_deleted = FALSE;

-- +goose Down

DROP INDEX IF EXISTS idx_files_path_trgm;
-- Extension is intentionally left in place: it may be referenced by
-- other indexes and dropping it is destructive.
