-- +goose Up

-- Per-user toggle for selection checkboxes in the web file browser (Issue #69
-- follow-up). Default TRUE so users discover the multi-select feature; they
-- can hide the column/overlay via the settings page once they know about it.
ALTER TABLE users
    ADD COLUMN show_file_checkboxes BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS show_file_checkboxes;
