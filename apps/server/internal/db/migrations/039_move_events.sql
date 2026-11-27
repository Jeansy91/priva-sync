-- +goose Up

-- ADR-0065: a directory move rides the changes feed as one prefix event
-- instead of one row per descendant. move_events.seq is drawn from the
-- same sequence as files.seq so both sources share one total order.
CREATE TABLE move_events (
    id           BIGSERIAL PRIMARY KEY,
    sync_root_id UUID NOT NULL,
    old_prefix   TEXT NOT NULL,
    new_prefix   TEXT NOT NULL,
    seq          BIGINT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_move_events_sync_root_seq ON move_events(sync_root_id, seq);

-- The rows a directory move rewrites (the dir row itself + every descendant)
-- must not bump seq: the move_events row above is the single feed signal
-- for the whole subtree, so a fresh seq per row would flood the feed again.
-- Session-scoped, so it never affects a concurrent transaction on another
-- connection, and current_setting(..., true) leaves every ordinary
-- INSERT/UPDATE that never touches the GUC unaffected.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assign_files_seq()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND current_setting('app.suppress_seq_bump', true) = 'on' THEN
        NEW.seq = OLD.seq;
    ELSE
        NEW.seq = nextval('files_seq_seq');
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down

DROP INDEX IF EXISTS idx_move_events_sync_root_seq;
DROP TABLE IF EXISTS move_events;

-- CREATE OR REPLACE, not DROP -- the files_assign_seq trigger still
-- references this function and a DROP would fail while it's attached.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assign_files_seq()
RETURNS TRIGGER AS $$
BEGIN
    NEW.seq = nextval('files_seq_seq');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
