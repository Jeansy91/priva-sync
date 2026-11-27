-- +goose Up

-- job_runs: zuletzt durchgefuehrter Lauf pro benanntem Background-Job.
-- Single row per name (UPSERT). Tracks status for the admin job overview.
CREATE TABLE job_runs (
    name         TEXT PRIMARY KEY,
    started_at   TIMESTAMPTZ NOT NULL,
    finished_at  TIMESTAMPTZ,
    status       TEXT NOT NULL,          -- running | success | error
    message      TEXT,
    items        INTEGER NOT NULL DEFAULT 0
);

-- +goose Down

DROP TABLE IF EXISTS job_runs;
