-- +goose Up

-- ADR-0002 / SPEC §5.5: Delta-Sync block-signature cache. delta-request hashes
-- the server file on-the-fly today; this column lets UploadFile persist the
-- signatures once at write time so the hot read path serves them straight from
-- the row. Nullable on purpose: NULL means "unknown" (pre-migration rows, or
-- files at/below the delta threshold) and delta-request falls back to on-the-fly
-- hashing — never "no blocks". Lives on the file row (no chunk table, no GC) so
-- the cache is atomic with every mutation that touches the bytes.
ALTER TABLE files ADD COLUMN block_hashes JSONB;

-- +goose Down

ALTER TABLE files DROP COLUMN IF EXISTS block_hashes;
