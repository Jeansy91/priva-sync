-- +goose Up

-- Ablauf je Public-Link statt je Aggregat: Lese- und Schreib-Link haben
-- unabhaengige Lebenszyklen (ADR-0015), also auch unabhaengige Limits.
-- shares.expires_at bleibt als Aggregat-Limit fuer Alt-Freigaben bestehen.
ALTER TABLE shares ADD COLUMN IF NOT EXISTS read_expires_at  TIMESTAMPTZ;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS write_expires_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE shares DROP COLUMN IF EXISTS write_expires_at;
ALTER TABLE shares DROP COLUMN IF EXISTS read_expires_at;
