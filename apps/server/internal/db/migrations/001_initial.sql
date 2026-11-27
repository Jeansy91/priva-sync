-- +goose Up

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ─── Users ───────────────────────────────────────────────────────────────────

CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT        NOT NULL UNIQUE,
    email         TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    totp_secret   TEXT,                              -- NULL = 2FA deaktiviert
    totp_enabled  BOOLEAN     NOT NULL DEFAULT FALSE,
    role          TEXT        NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    quota_bytes   BIGINT      NOT NULL DEFAULT 0,    -- 0 = unbegrenzt
    used_bytes    BIGINT      NOT NULL DEFAULT 0,
    is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
    email_notify  BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Login Attempts (für Rate Limiting / Lockout) ─────────────────────────────

CREATE TABLE login_attempts (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    username   TEXT        NOT NULL,
    ip_address INET,
    succeeded  BOOLEAN     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_login_attempts_username ON login_attempts(username, created_at DESC);
CREATE INDEX idx_login_attempts_ip       ON login_attempts(ip_address, created_at DESC);

-- ─── Refresh Tokens ───────────────────────────────────────────────────────────

CREATE TABLE refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT        NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);

-- ─── Sync Roots ───────────────────────────────────────────────────────────────

CREATE TABLE sync_roots (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,   -- Anzeigename, z.B. "Dokumente"
    path       TEXT        NOT NULL,   -- Server-seitiger relativer Pfad
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, name),
    UNIQUE (user_id, path)
);

-- ─── Clients ──────────────────────────────────────────────────────────────────

CREATE TABLE clients (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    platform   TEXT        NOT NULL CHECK (platform IN ('windows', 'macos', 'linux', 'mobile')),
    last_seen  TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_clients_user ON clients(user_id);

-- ─── Files ────────────────────────────────────────────────────────────────────

CREATE TABLE files (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sync_root_id UUID        NOT NULL REFERENCES sync_roots(id) ON DELETE CASCADE,
    path         TEXT        NOT NULL,            -- relativer Pfad ab Sync-Root
    size         BIGINT      NOT NULL DEFAULT 0,
    mtime        TIMESTAMPTZ NOT NULL,
    content_hash BYTEA,                           -- BLAKE3 (32 Bytes)
    is_deleted   BOOLEAN     NOT NULL DEFAULT FALSE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (sync_root_id, path)
);

CREATE INDEX idx_files_sync_root ON files(sync_root_id);
CREATE INDEX idx_files_user      ON files(user_id);
CREATE INDEX idx_files_hash      ON files(content_hash) WHERE content_hash IS NOT NULL;

-- ─── Client File States ───────────────────────────────────────────────────────

CREATE TABLE client_file_states (
    client_id  UUID        NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    file_id    UUID        NOT NULL REFERENCES files(id)   ON DELETE CASCADE,
    known_hash BYTEA,
    synced_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (client_id, file_id)
);

-- ─── Conflicts ────────────────────────────────────────────────────────────────

CREATE TABLE conflicts (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    file_id       UUID        NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    conflict_path TEXT        NOT NULL,   -- Pfad der Konflikt-Kopie
    client_id     UUID        REFERENCES clients(id) ON DELETE SET NULL,
    resolved      BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at   TIMESTAMPTZ
);

CREATE INDEX idx_conflicts_file     ON conflicts(file_id);
CREATE INDEX idx_conflicts_resolved ON conflicts(resolved) WHERE NOT resolved;

-- ─── Shares ───────────────────────────────────────────────────────────────────

CREATE TABLE shares (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    path          TEXT        NOT NULL,
    share_type    TEXT        NOT NULL CHECK (share_type IN ('public', 'user')),
    token         TEXT        UNIQUE,             -- öffentliche Shares
    password_hash TEXT,                           -- optionaler Passwortschutz
    expires_at    TIMESTAMPTZ,
    can_write     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_shares_user  ON shares(user_id);
CREATE INDEX idx_shares_token ON shares(token) WHERE token IS NOT NULL;

CREATE TABLE share_permissions (
    share_id  UUID    NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    user_id   UUID    NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    can_write BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (share_id, user_id)
);

-- ─── Audit Log ────────────────────────────────────────────────────────────────

CREATE TABLE audit_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        REFERENCES users(id) ON DELETE SET NULL,
    event_type TEXT        NOT NULL,
    ip_address INET,
    user_agent TEXT,
    details    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_log_user ON audit_log(user_id, created_at DESC);
CREATE INDEX idx_audit_log_type ON audit_log(event_type, created_at DESC);
CREATE INDEX idx_audit_log_time ON audit_log(created_at DESC);

-- ─── Trigger: updated_at automatisch setzen ───────────────────────────────────

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER files_updated_at
    BEFORE UPDATE ON files
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down

DROP TRIGGER IF EXISTS files_updated_at ON files;
DROP TRIGGER IF EXISTS users_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS share_permissions;
DROP TABLE IF EXISTS shares;
DROP TABLE IF EXISTS conflicts;
DROP TABLE IF EXISTS client_file_states;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS clients;
DROP TABLE IF EXISTS sync_roots;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS login_attempts;
DROP TABLE IF EXISTS users;
