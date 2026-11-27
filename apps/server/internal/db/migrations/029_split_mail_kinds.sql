-- +goose Up

-- Issue #122: der Sammel-Kind "mail_send" wird in zwei typisierte Kinds
-- aufgeteilt, damit jeder Mail-Handler an einen eigenen Payload-Typ bindet
-- statt intern auf ein Discriminator-Feld zu dispatchen. Bestehende (auch
-- in-flight) Rows werden anhand ihres bisherigen payload->>'kind' umgezogen,
-- sodass der Worker sie nach dem Deploy sofort dem neuen Handler zuordnet.
UPDATE outbox
SET kind = CASE payload->>'kind'
    WHEN 'sync_conflict' THEN 'mail_sync_conflict'
    WHEN 'quota_warning' THEN 'mail_quota_warning'
    ELSE kind
END
WHERE kind = 'mail_send';

-- +goose Down

UPDATE outbox
SET kind = 'mail_send'
WHERE kind IN ('mail_sync_conflict', 'mail_quota_warning');
