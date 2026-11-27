-- +goose Up

CREATE TABLE passkey_credentials (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA       NOT NULL UNIQUE,
    credential    JSONB       NOT NULL,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ
);

CREATE INDEX idx_passkey_credentials_user ON passkey_credentials(user_id);

-- +goose Down
DROP TABLE IF EXISTS passkey_credentials;
