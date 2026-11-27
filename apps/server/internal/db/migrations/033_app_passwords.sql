-- +goose Up

-- App-Passwoerter fuer WebDAV (ADR-0040). Basic-Auth kann keinen zweiten
-- Faktor abfragen, also darf dort nicht das Kontopasswort gelten: sonst ist
-- ein Konto mit aktivem TOTP ueber Port 8081 mit reinem Passwort uebernehmbar.
--
-- `secret_hash` ist SHA-256 ueber ein serverseitig erzeugtes Zufallsgeheimnis
-- (>= 160 bit), nicht Argon2id -- gleiche Begruendung wie bei backup_codes:
-- das Geheimnis ist nicht nutzergewaehlt und nicht ratbar, und der Hash muss
-- pro WebDAV-Request direkt nachschlagbar sein.
CREATE TABLE app_passwords (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    secret_hash  TEXT        NOT NULL UNIQUE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_app_passwords_user ON app_passwords(user_id, created_at DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_app_passwords_user;
DROP TABLE IF EXISTS app_passwords;
