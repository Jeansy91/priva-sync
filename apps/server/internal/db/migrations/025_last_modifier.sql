-- +goose Up

-- ADR-0013: Letzter Bearbeiter pro Datei. Nullable, weil bestehende Rows
-- niemanden ausser dem Eigentuemer als Akteur kennen — die Detail-UI
-- rendert die Anzeige nur, wenn last_modifier ≠ owner. ON DELETE SET NULL,
-- damit das Loeschen eines Users niemals File-Rows kaskadiert.
ALTER TABLE files
    ADD COLUMN last_modifier_user_id UUID NULL
        REFERENCES users(id) ON DELETE SET NULL;

-- +goose Down

ALTER TABLE files DROP COLUMN IF EXISTS last_modifier_user_id;
