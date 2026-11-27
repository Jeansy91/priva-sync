-- +goose Up

-- Bevorzugte UI- und Mail-Sprache pro User. Default 'en' (Issue #5):
-- die UI startet auf Englisch, bis der User aktiv umschaltet.
ALTER TABLE users
    ADD COLUMN preferred_language TEXT NOT NULL DEFAULT 'en'
    CHECK (preferred_language IN ('de', 'en'));

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS preferred_language;
