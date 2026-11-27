-- +goose Up

-- Subtree selection by byte range (#194) is indexable; the LIKE it replaced
-- was not. Postgres derives range bounds only from a pattern fixed at plan
-- time, and every one of these patterns arrived as a parameter, so listing a
-- directory scanned the whole root regardless of which index existed.
--
-- Measured on a one-million-row root, listing one directory: 430ms -> 79ms,
-- reading 2000 index entries instead of every row (#193).
--
-- text_pattern_ops, not the default operator class: the database collates as
-- en_US.utf8, where "sorts between X and X's successor" is a different set
-- from "starts with X". The queries compare with ~>=~ / ~<~ for the same
-- reason.
--
-- Partial on is_deleted = FALSE, matching every query that uses it. The one
-- subtree query that also reads tombstones (the Shared-Mount delta feed)
-- keeps ordering by seq and is served by idx_files_sync_root_seq.
--
-- Not CONCURRENTLY: migrations run at startup before the server serves, so
-- the table lock costs nothing that is not already unavailable, and goose
-- would need the statement outside a transaction.
CREATE INDEX IF NOT EXISTS idx_files_path_prefix
    ON files (sync_root_id, path text_pattern_ops)
    WHERE is_deleted = FALSE;

-- +goose Down

DROP INDEX IF EXISTS idx_files_path_prefix;
