-- +goose Up

ALTER TABLE files ADD COLUMN server_version BIGINT NOT NULL DEFAULT 0;

-- Automatically increment server_version on every UPDATE so clients can detect
-- stale uploads without relying on wall-clock timestamps.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION increment_server_version()
RETURNS TRIGGER AS $$
BEGIN
    NEW.server_version = OLD.server_version + 1;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER files_increment_version
    BEFORE UPDATE ON files
    FOR EACH ROW
    EXECUTE FUNCTION increment_server_version();

-- +goose Down

DROP TRIGGER IF EXISTS files_increment_version ON files;
DROP FUNCTION IF EXISTS increment_server_version;
ALTER TABLE files DROP COLUMN IF EXISTS server_version;
