-- +goose Up

-- Tracks multi-part upload sessions for files > 10 MB.
-- Sessions expire after 24 hours; a background job purges orphaned chunks.
CREATE TABLE upload_sessions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sync_root_id    UUID        NOT NULL REFERENCES sync_roots(id) ON DELETE CASCADE,
    path            TEXT        NOT NULL,
    total_size      BIGINT      NOT NULL,
    content_hash    BYTEA,                              -- pre-computed by client (optional)
    chunk_size      BIGINT      NOT NULL DEFAULT 5242880, -- 5 MiB
    total_chunks    INT         NOT NULL,
    received_chunks INT[]       NOT NULL DEFAULT '{}',
    mtime           TIMESTAMPTZ,
    if_match        BIGINT,                             -- client's known server_version
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

CREATE INDEX idx_upload_sessions_user    ON upload_sessions(user_id);
CREATE INDEX idx_upload_sessions_expires ON upload_sessions(expires_at);

-- +goose Down

DROP TABLE IF EXISTS upload_sessions;
