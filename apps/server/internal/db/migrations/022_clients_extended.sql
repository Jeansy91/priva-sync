-- +goose Up

-- Bisher ungenutzte clients-Tabelle um Identifikationsfelder erweitern, damit
-- jeder Desktop-/Mobile-Client beim Login eine Row anlegt und Admin eine
-- Geraete-Uebersicht (User, OS, Version, IP, Letzter Zugriff) sieht.

-- Tabelle ist bisher in keinem Code-Pfad befuellt; Truncate ist sicher und
-- vermeidet eine zweistufige NOT-NULL-Migration.
TRUNCATE TABLE clients CASCADE;

ALTER TABLE clients
    ADD COLUMN device_id      TEXT NOT NULL,
    ADD COLUMN os_version     TEXT,
    ADD COLUMN client_version TEXT,
    ADD COLUMN last_ip        INET;

ALTER TABLE clients
    ADD CONSTRAINT clients_user_device_unique UNIQUE (user_id, device_id);

CREATE INDEX idx_clients_last_seen ON clients(last_seen DESC NULLS LAST);

-- +goose Down

DROP INDEX IF EXISTS idx_clients_last_seen;

ALTER TABLE clients
    DROP CONSTRAINT IF EXISTS clients_user_device_unique;

ALTER TABLE clients
    DROP COLUMN IF EXISTS last_ip,
    DROP COLUMN IF EXISTS client_version,
    DROP COLUMN IF EXISTS os_version,
    DROP COLUMN IF EXISTS device_id;
