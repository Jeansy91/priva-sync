# ADR-0004: Trash zählt zur Quota (kein separater Topf)

**Status:** Accepted
**Datum:** 2026-04-30

## Kontext

Soft-Deletes landen im Trash und werden nach 30 Tagen automatisch endgültig
gelöscht. Frage: zählen sie zur Quota?

Ohne Begrenzung horten User beliebig viele "gelöschte" Dateien — die Disk
läuft voll, der Operator zahlt. Ein separater Trash-Topf mit Auto-Purge
beim Überlauf ist intransparent: gelöschte Dateien verschwinden
eigenmächtig, obwohl der User sie noch wiederherstellen wollte.

## Entscheidung

`UsedBytes + TrashBytes` zählen gegen `QuotaBytes`. Es gibt **keinen**
separaten Trash-Cap und **keinen** automatischen FIFO-Purge.

- Quota-Gate vor jedem Upload: `(used + trash) >= quota` → HTTP 507.
- Wer voll ist, muss Trash leeren oder den 30-Tage-Auto-Purge abwarten.
- Web-UI zeigt einen einzigen `(used + trash) / quota`-Balken; Trash wird
  daneben als reine Datenmenge ausgewiesen (kein eigener Balken).
- Threshold-Mails (80 %, 95 %) basieren auf effektiver Auslastung
  (`used + trash`).

## Konsequenzen

**Positiv:**
- Eine Zahl, eine Wahrheit. User versteht sofort, warum Upload abgelehnt
  wird.
- Keine eigenmächtigen Löschungen.
- Conflict-Loser-Kopien (siehe [ADR-0003](0003-conflict-loser-copy.md))
  schlagen ebenfalls voll auf die Quota durch — konsistent.

**Negativ:**
- User, die viel löschen, kommen schneller ins Limit.
- Threshold-Mails können knapp nach einem großen Delete eskalieren —
  Hysterese im **Quota State** (siehe `docs/glossary.md`) dämpft.
