-- +goose Up

-- Profilbild pro User. Pfad ist relativ zu {data_path}/avatars/, NULL = kein
-- Avatar gesetzt. Avatare liegen ausserhalb der User-Sync-Roots, damit sie
-- nicht versehentlich syncen, und zaehlen nicht zur Quota.
ALTER TABLE users ADD COLUMN avatar_path TEXT;

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS avatar_path;
