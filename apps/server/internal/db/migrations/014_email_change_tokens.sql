-- +goose Up

-- Pending email changes — confirmed via link to the *new* address before the
-- users.email column is updated. Only the latest pending change per user is
-- meaningful, but we keep the table simple (unique on token_hash only) and let
-- the handler delete prior rows on a new request.
CREATE TABLE email_change_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    new_email  TEXT        NOT NULL,
    token_hash TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_email_change_tokens_user    ON email_change_tokens(user_id);
CREATE INDEX idx_email_change_tokens_expires ON email_change_tokens(expires_at);

-- +goose Down
DROP TABLE IF EXISTS email_change_tokens;
