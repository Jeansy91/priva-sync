-- +goose Up

-- Quota-Warnung: merken, ob eine 80%/95%-Mail bereits verschickt wurde,
-- damit Nutzer nicht bei jedem Upload neu benachrichtigt werden.
-- Werden zurueckgesetzt, sobald die Nutzung wieder unter die Schwelle faellt.
ALTER TABLE users ADD COLUMN quota_warned_80 BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN quota_warned_95 BOOLEAN NOT NULL DEFAULT FALSE;

-- share_type='user' benoetigt einen Empfaenger. Bisher war das Feld als
-- Enum-Wert definiert, ohne zugehoerige Ziel-Spalte — damit konnte die
-- Share-received-Mail nicht ausgeloest werden.
ALTER TABLE shares ADD COLUMN target_user_id UUID REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX idx_shares_target_user ON shares(target_user_id) WHERE target_user_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_shares_target_user;
ALTER TABLE shares DROP COLUMN IF EXISTS target_user_id;
ALTER TABLE users  DROP COLUMN IF EXISTS quota_warned_95;
ALTER TABLE users  DROP COLUMN IF EXISTS quota_warned_80;
