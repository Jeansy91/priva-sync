-- +goose Up

-- Trash zaehlt nicht zur primaeren Quota, wird aber separat budgetiert
-- (max 25% der Quota). Ein zusaetzliches Feld macht das Tracking explizit
-- und vermeidet teure SUM(size)-Aggregationen pro Anfrage.
ALTER TABLE users ADD COLUMN trash_bytes BIGINT NOT NULL DEFAULT 0;

-- Initial-Backfill: bestehende Trash-Files in den Counter ziehen.
UPDATE users SET trash_bytes = COALESCE((
    SELECT SUM(f.size)
    FROM files f
    WHERE f.user_id = users.id
      AND f.is_deleted = TRUE
      AND f.trash_path IS NOT NULL
), 0);

-- Recompute-Backfill auch fuer used_bytes — damit Drift aus alten Bugs
-- (siehe #15: persistConflict ohne Quota-Delta) sofort weg ist.
UPDATE users SET used_bytes = COALESCE((
    SELECT SUM(f.size)
    FROM files f
    WHERE f.user_id = users.id
      AND f.is_deleted = FALSE
), 0);

-- +goose Down

ALTER TABLE users DROP COLUMN IF EXISTS trash_bytes;
