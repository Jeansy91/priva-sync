-- +goose Up

-- ADR-0015: eine bearbeitbare User-Freigabe pro Pfad wird zum Aggregat
-- (shares) mit Empfaenger-Grants. Jeder Grant traegt can_write + Ablauf je
-- Empfaenger. Additive Migration: alte Spalten (target_user_id/token/can_write)
-- bleiben vorerst, damit die noch nicht migrierten Lese-/Schreibpfade
-- weiterlaufen; der Spalten-Drop folgt, sobald alle Leser auf Grants stehen.
CREATE TABLE share_grants (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    share_id       UUID        NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    target_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    can_write      BOOLEAN     NOT NULL DEFAULT FALSE,
    expires_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (share_id, target_user_id)
);

-- Empfaenger-Lookup (ListMountsForTarget) faehrt ueber target_user_id.
CREATE INDEX idx_share_grants_target ON share_grants(target_user_id);

-- Public-Freigabe: zwei Tokens statt einem. read_token immer, write_token nur
-- fuer Verzeichnisse (Dateien haben keinen Schreibzugriff).
ALTER TABLE shares ADD COLUMN read_token  TEXT;
ALTER TABLE shares ADD COLUMN write_token TEXT;

-- Backfill: jede bestehende User-Share-Zeile wird ihr eigener Grant. grant.id =
-- share.id haelt den Empfaenger-Mount-Anker (ADR-0010/0011) stabil, damit der
-- Desktop nicht prunet.
INSERT INTO share_grants (id, share_id, target_user_id, can_write, expires_at, created_at)
SELECT id, id, target_user_id, can_write, expires_at, created_at
FROM shares
WHERE share_type = 'user' AND target_user_id IS NOT NULL;

-- Backfill Public-Tokens: alter Token bleibt als Lese-Link; war er ein
-- Schreib-Link (can_write), wirkt er zusaetzlich als write_token, damit kein
-- bereits verteilter Link tot faellt.
UPDATE shares SET read_token = token
    WHERE share_type = 'public' AND token IS NOT NULL;
UPDATE shares SET write_token = token
    WHERE share_type = 'public' AND can_write = TRUE AND token IS NOT NULL;

-- +goose Down

ALTER TABLE shares DROP COLUMN IF EXISTS write_token;
ALTER TABLE shares DROP COLUMN IF EXISTS read_token;
DROP TABLE IF EXISTS share_grants;
