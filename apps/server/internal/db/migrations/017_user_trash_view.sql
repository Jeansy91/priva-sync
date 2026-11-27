-- +goose Up

-- Pro-User-Einstellung fuer die Papierkorb-Ansicht (Liste vs. Ordneransicht).
-- Default 'list' = neutrales Verhalten, bestehende User sehen weiterhin die
-- Listenansicht beim ersten Login nach dem Update.
ALTER TABLE users
    ADD COLUMN trash_view TEXT NOT NULL DEFAULT 'list'
        CHECK (trash_view IN ('list', 'tree'));

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS trash_view;
