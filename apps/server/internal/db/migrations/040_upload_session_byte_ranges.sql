-- +goose Up

-- ADR-0066: chunk size is chosen per chunk by the client, so a chunk can no
-- longer be addressed by index * a session-wide constant. Coverage moves from
-- an index bitmap to the byte ranges actually written.
ALTER TABLE upload_sessions ADD COLUMN received_ranges INT8MULTIRANGE NOT NULL DEFAULT '{}';

-- A chunked upload in flight across this migration cannot be resumed under
-- the old addressing scheme either way — its offsets were index * chunk_size,
-- a mapping this migration removes. Same accepted loss as ADR-0045.
ALTER TABLE upload_sessions DROP COLUMN chunk_size;
ALTER TABLE upload_sessions DROP COLUMN total_chunks;
ALTER TABLE upload_sessions DROP COLUMN received_chunks;

-- +goose Down

ALTER TABLE upload_sessions ADD COLUMN chunk_size BIGINT NOT NULL DEFAULT 5242880;
ALTER TABLE upload_sessions ADD COLUMN total_chunks INT NOT NULL DEFAULT 0;
ALTER TABLE upload_sessions ADD COLUMN received_chunks INT[] NOT NULL DEFAULT '{}';
ALTER TABLE upload_sessions DROP COLUMN received_ranges;
