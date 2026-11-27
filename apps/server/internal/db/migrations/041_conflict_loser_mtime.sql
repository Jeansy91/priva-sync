-- +goose Up

-- The loser copy's own modification time (what the client had on disk), so a
-- resolve dialog can set both versions side by side. NULL for conflicts that
-- predate this column.
ALTER TABLE conflicts ADD COLUMN loser_mtime TIMESTAMPTZ;

-- +goose Down
ALTER TABLE conflicts DROP COLUMN loser_mtime;
