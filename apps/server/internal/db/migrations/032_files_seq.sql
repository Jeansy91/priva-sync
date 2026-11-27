-- +goose Up

-- ADR-0036: monotonic change-sequence, independent of wall-clock updated_at.
-- A backwards clock correction (NTP jump) can otherwise assign a smaller
-- updated_at to a new change than one already delivered, silently hiding it
-- from a client cursor that has already advanced past that point.

ALTER TABLE files ADD COLUMN seq BIGINT;

-- Backfill before the trigger exists, so a plain SET sticks and future
-- nextval() calls (started below, after the max backfilled value) cannot
-- collide with these values.
WITH ordered AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY updated_at ASC) AS rn
    FROM files
)
UPDATE files SET seq = ordered.rn
FROM ordered
WHERE files.id = ordered.id;

CREATE SEQUENCE files_seq_seq;
SELECT setval('files_seq_seq', COALESCE((SELECT MAX(seq) FROM files), 1), (SELECT MAX(seq) FROM files) IS NOT NULL);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assign_files_seq()
RETURNS TRIGGER AS $$
BEGIN
    NEW.seq = nextval('files_seq_seq');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER files_assign_seq
    BEFORE INSERT OR UPDATE ON files
    FOR EACH ROW
    EXECUTE FUNCTION assign_files_seq();

ALTER TABLE files ALTER COLUMN seq SET NOT NULL;

CREATE INDEX idx_files_sync_root_seq ON files(sync_root_id, seq);

-- +goose Down

DROP INDEX IF EXISTS idx_files_sync_root_seq;
DROP TRIGGER IF EXISTS files_assign_seq ON files;
DROP FUNCTION IF EXISTS assign_files_seq;
DROP SEQUENCE IF EXISTS files_seq_seq;
ALTER TABLE files DROP COLUMN IF EXISTS seq;
