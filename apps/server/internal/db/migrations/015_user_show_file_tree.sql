-- +goose Up

-- Pro-User-Toggle fuer die File-Tree-Sidebar im Web-Frontend (Issue #19).
-- Default TRUE: bestehende User sehen den Tree sofort, koennen ihn in den
-- Einstellungen abschalten.
ALTER TABLE users
    ADD COLUMN show_file_tree BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS show_file_tree;
