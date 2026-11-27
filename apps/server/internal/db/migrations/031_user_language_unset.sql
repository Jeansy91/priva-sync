-- +goose Up

-- Issue: default 'en' (013) pinned every newly created user to English,
-- because admin.CreateUser never passes preferred_language — this
-- overwrote the correctly detected browser language after first login.
-- Empty string instead of 'en' as "not set yet": the mailer and frontend
-- already treat that as a fallback signal (see mail.Mailer.SendTemplate
-- doc), so the client keeps applying its own browser/OS detection
-- instead of it being overwritten.
ALTER TABLE users
    ALTER COLUMN preferred_language SET DEFAULT '';

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_preferred_language_check;
ALTER TABLE users
    ADD CONSTRAINT users_preferred_language_check
    CHECK (preferred_language IN ('', 'de', 'en'));

-- +goose Down

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_preferred_language_check;
UPDATE users SET preferred_language = 'en' WHERE preferred_language = '';
ALTER TABLE users
    ADD CONSTRAINT users_preferred_language_check
    CHECK (preferred_language IN ('de', 'en'));
ALTER TABLE users
    ALTER COLUMN preferred_language SET DEFAULT 'en';
