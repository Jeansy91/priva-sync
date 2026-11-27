-- +goose Up

-- Outbox: transaktional erfasste Side-Effects (Storage-Rename, SSE-Publish,
-- Mail), die ein Worker nach Commit ausführt. Same-TX-Insert garantiert,
-- dass kein Side-Effect verloren geht; der Worker zieht entweder per
-- Same-TX-Hook sofort oder per Fallback-Poll nach Crash.
--
-- group_id sortiert Events einer logischen Operation (z.B. ein Upload):
-- der Worker arbeitet jede Gruppe streng in seq-Reihenfolge ab und
-- blockiert ein zweites Event derselben Gruppe bis das erste done ist.
CREATE TABLE outbox (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id         UUID NOT NULL,
    seq              INTEGER NOT NULL,
    kind             TEXT NOT NULL,
    payload          JSONB NOT NULL,
    attempts         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error       TEXT,
    done_at          TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (group_id, seq)
);

CREATE INDEX idx_outbox_pending
    ON outbox (next_attempt_at)
    WHERE done_at IS NULL;

CREATE INDEX idx_outbox_group_pending
    ON outbox (group_id, seq)
    WHERE done_at IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_outbox_group_pending;
DROP INDEX IF EXISTS idx_outbox_pending;
DROP TABLE IF EXISTS outbox;
