# ADR-0003: Konflikte als Loser-Kopie + DB-Row

**Status:** Accepted
**Datum:** 2026-04-30

## Kontext

Wenn zwei Clients dieselbe Datei ändern, bevor einer den anderen gesehen
hat, muss der Server entscheiden, was mit den konkurrierenden Bytes
passiert.

Designprinzip: **Korrektheit vor Geschwindigkeit, kein Datenverlust.** Eine
stumme Last-Write-Wins-Strategie verbietet sich. Ein hartes Reject (HTTP
409) würde verlierende Bytes nur lokal beim Client lassen — wenn der User
das vergisst, gehen sie trotzdem verloren.

## Entscheidung

Beide Versionen behalten. Verlierender Client landet seine Bytes unter
`<orig>.conflict-<ts>.<ext>`. Eine `conflicts`-DB-Row hält den Bezug zur
Original-`files`-Row.

- **Version-Konflikt** (`If-Match` schlägt fehl): HTTP **409** ohne
  Conflict-Row — Client muss neu syncen und re-upload entscheiden.
- **Echter Konflikt** (paralleler Write, beide committed): Loser-Kopie +
  Row, Client erhält **2xx** plus SSE `ConflictCreated`.
- Auflösung im Desktop- oder Web-Client: Diff-Ansicht (Text), Auswahl,
  Loser-Datei wird gelöscht, Row geschlossen, Clients re-syncen.

Loser-Bytes zählen zur **Quota** des Besitzers (siehe
[ADR-0004](0004-trash-counts-toward-quota.md)) — sichtbar machen statt
verstecken.

## Konsequenzen

**Positiv:**
- Kein Datenverlust, auch wenn der User die Auflösung Wochen aufschiebt.
- Trennung Version-Conflict (Protokoll-Schicht) vs. Domain-Conflict (User-
  Schicht) ist im Code explizit (`Sync Coordinator` entscheidet).

**Negativ:**
- Bytes verdoppeln sich; User mit knapper Quota können sich selbst
  aussperren.
- Loser-Pfad-Namen sind hässlich, aber eindeutig unterscheidbar.
- Auflösungs-UI muss in zwei Apps gepflegt werden (Desktop + Web).
