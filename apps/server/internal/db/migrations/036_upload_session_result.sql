-- +goose Up

-- Die Session-Zeile wird zum Ergebnis-Record des Chunked-Uploads (#201).
--
-- Bisher loeschte das Complete die Zeile, sobald es fertig war. Damit gab es
-- nichts mehr, woran ein wiederholtes Complete haette erkennen koennen, dass
-- die Arbeit bereits getan ist -- es assemblierte die komplette Datei erneut,
-- oder scheiterte an den inzwischen geloeschten Chunks. Der Desktop-Client
-- wiederholt bis zu zwoelf Stunden lang.
--
-- `result_kind` bildet die drei Ausgaenge des Coordinators ab
-- ('ok' | 'version_mismatch' | 'conflict'), `error_code` haelt bei einem
-- terminalen Fehlschlag den bestehenden API-Fehlercode -- als Code, nicht als
-- freien Text, damit die UI ihn uebersetzen kann.
ALTER TABLE upload_sessions
    ADD COLUMN status                TEXT NOT NULL DEFAULT 'open',
    ADD COLUMN result_kind           TEXT,
    ADD COLUMN result_file_id        UUID,
    ADD COLUMN result_server_version BIGINT,
    ADD COLUMN result_content_hash   BYTEA,
    ADD COLUMN result_conflict_path  TEXT,
    ADD COLUMN error_code            TEXT,
    ADD COLUMN completed_at          TIMESTAMPTZ;
