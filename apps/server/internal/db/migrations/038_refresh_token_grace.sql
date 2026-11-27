-- +goose Up

-- Refresh-token rotation used to delete the old row outright. A client that
-- fires several requests in parallel refreshes more than once with the same
-- cookie -- every call but the first then presented an already-deleted token
-- and the session ended for no reason (ADR-0046).
--
-- Rotation now marks the row instead. Inside a short grace window the old
-- token still buys an access token (but no second rotation); past it the row
-- is rejected and deleted. NULL = current, never rotated.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS rotated_at TIMESTAMPTZ;

-- The sweep after each rotation deletes this user's rows past their grace.
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_rotated ON refresh_tokens (user_id, rotated_at)
    WHERE rotated_at IS NOT NULL;
