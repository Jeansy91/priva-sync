-- +goose Up

-- Das Complete eines Chunked-Uploads laeuft nicht mehr im Request, sondern in
-- einem In-Process-Worker (#202). Damit braucht die Session-Zeile zwei
-- Zwischenzustaende und einen Versuchszaehler.
--
-- 'queued'  -- angenommen, wartet auf einen freien Worker
-- 'running' -- ein Worker haelt die Session
--
-- Der Zaehler trennt permanente von transienten Fehlschlaegen: ein fehlender
-- Chunk oder ein Hash-Mismatch ist endgueltig, ein I/O-Fehler darf wiederholt
-- werden -- aber nicht unbegrenzt. `last_error_code` haelt den Grund des
-- letzten gescheiterten Versuchs, waehrend die Session noch lebt;
-- `error_code` bleibt dem terminalen Fehlschlag vorbehalten.
ALTER TABLE upload_sessions
    ADD COLUMN attempts        INT NOT NULL DEFAULT 0,
    ADD COLUMN last_error_code TEXT;

-- Der Boot-Sweep sucht genau nach diesen beiden Zustaenden. Bei genau einer
-- Serverinstanz (ADR-0039) ist alles, was nach einem Neustart noch 'running'
-- ist, gestrandet und gehoert requeued.
CREATE INDEX IF NOT EXISTS idx_upload_sessions_in_flight
    ON upload_sessions (status)
    WHERE status IN ('queued', 'running');
