-- +goose Up
ALTER TABLE shares ADD COLUMN sync_root_id UUID REFERENCES sync_roots(id) ON DELETE CASCADE;
CREATE INDEX idx_shares_sync_root ON shares(sync_root_id) WHERE sync_root_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_shares_sync_root;
ALTER TABLE shares DROP COLUMN sync_root_id;
