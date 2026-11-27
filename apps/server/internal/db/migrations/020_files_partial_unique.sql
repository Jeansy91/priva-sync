-- +goose Up

-- Partial-Unique (sync_root_id, path) WHERE is_deleted = FALSE.
-- Erlaubt mehrere getrashte Rows am selben Pfad neben einer aktiven Row,
-- damit der Restore-on-Conflict-Pfad (live Row am ursprünglichen Pfad
-- belegt während eine getrashte Row dorthin restaureriert werden soll)
-- ein real darstellbarer Zustand ist.
ALTER TABLE files DROP CONSTRAINT files_sync_root_id_path_key;

CREATE UNIQUE INDEX files_sync_root_id_path_live_key
    ON files (sync_root_id, path)
    WHERE is_deleted = FALSE;

-- +goose Down

DROP INDEX IF EXISTS files_sync_root_id_path_live_key;
ALTER TABLE files ADD CONSTRAINT files_sync_root_id_path_key UNIQUE (sync_root_id, path);
