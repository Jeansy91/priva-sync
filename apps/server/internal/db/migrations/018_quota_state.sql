-- +goose Up

-- Replace the pair of boolean warning flags (quota_warned_80, quota_warned_95)
-- with a single sum-type column quota_state that names the current usage band
-- exhaustively (ok / warning80 / critical95). The state-machine in Go owns the
-- transitions; the DB just stores the latest band so mails fire only when the
-- band escalates.
CREATE TYPE quota_state AS ENUM ('ok', 'warning80', 'critical95');

ALTER TABLE users ADD COLUMN quota_state quota_state NOT NULL DEFAULT 'ok';

-- Backfill from the prior bool pair: 95 wins over 80, otherwise ok.
UPDATE users SET quota_state = CASE
    WHEN quota_warned_95 THEN 'critical95'::quota_state
    WHEN quota_warned_80 THEN 'warning80'::quota_state
    ELSE 'ok'::quota_state
END;

ALTER TABLE users DROP COLUMN quota_warned_95;
ALTER TABLE users DROP COLUMN quota_warned_80;

-- +goose Down

ALTER TABLE users ADD COLUMN quota_warned_80 BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN quota_warned_95 BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE users SET
    quota_warned_80 = quota_state IN ('warning80', 'critical95'),
    quota_warned_95 = quota_state = 'critical95';

ALTER TABLE users DROP COLUMN quota_state;
DROP TYPE quota_state;
