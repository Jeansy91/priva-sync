# ADR-0036: Changes-Feed mit monotoner Sequenz statt Timestamp-Cursor

**Status:** Accepted
**Datum:** 2026-08-24

## Kontext

`GetChangedSince` (`/sync-roots/{id}/changes`) liefert dem Desktop-Client alle
Änderungen seit dem letzten Sync — Cursor ist bisher `updated_at` (Wallclock),
Abfrage ohne `LIMIT`. Zwei Probleme, die bei Millionen-Datei-Roots real werden:

1. **Kein Pagination-Limit.** Erst-Sync oder ein langes Offline-Fenster liefert
   den kompletten Root in einer einzigen SQL-Query + einem einzigen
   JSON-Response — Speicherspitze auf Server und Client, kein Resume bei
   Verbindungsabbruch.
2. **Wallclock-Cursor ist nicht korrekt unter Uhr-Drift.** `updated_at` hängt
   von der DB-Serveruhr ab. Eine rückwärts korrigierte Uhr (NTP-Sprung) kann
   neuen Changes einen `updated_at` geben, der kleiner ist als bereits an den
   Client gelieferte Werte — der Client-Cursor hat den Punkt dann schon
   passiert und verpasst den Change dauerhaft, ohne Fehler.

`process_downloads` umgeht das Race zwischen Query und neuen Changes bereits
mit einem Trick (Wallclock vor der Query erfassen statt danach,
[reconcile.rs:64](../../apps/desktop/src-tauri/src/sync/reconcile.rs#L64)) —
das behebt das Zwischen-Query-Race, aber nicht die Uhr-Drift-Klasse von Bug.

## Entscheidung

`files` bekommt eine neue Spalte `seq BIGINT`, befüllt über
`BEFORE INSERT OR UPDATE`-Trigger aus einer pro-Root (oder global) laufenden
Postgres-Sequence — monoton, unabhängig von der Systemuhr.

`GetChangedSince` wird zu einem Keyset-paginierten Changes-Feed:

```sql
SELECT ... FROM files
 WHERE sync_root_id = $1 AND seq > $cursor
 ORDER BY seq ASC
 LIMIT $page_size
```

`seq` ersetzt `updated_at` als einzigen Cursor-Typ — sowohl für das äußere
Sync-Fenster (bisher `last_sync`-Timestamp) als auch für die Pagination
innerhalb einer Fetch-Runde. Der Wallclock-Vor-Query-Trick in
`process_downloads` entfällt, weil "seq > cursor" ohne Zeitfenster eindeutig
ist.

Index: `(sync_root_id, seq)`.

## Konsequenzen

**Positiv:**
- Uhr-Drift kann keine Changes mehr verschlucken — strukturelle Garantie
  statt Wallclock-Vertrauen.
- Ein Cursor-Konzept statt zwei (Wallclock-Fenster + Timestamp-Keyset) —
  weniger Fläche für Off-by-one- oder Tie-Bugs an der Fenstergrenze.
- Pagination macht Erst-Sync/lange-Offline-Fenster auf Millionen-Roots
  speicherbeschränkt statt einer unbegrenzten Antwort.

**Negativ:**
- Migration: neue Spalte + Trigger + Backfill für Bestandszeilen
  (`seq` nach `updated_at`-Reihenfolge vorbefüllen).
- Trigger muss sowohl INSERT als auch UPDATE abdecken — ein vergessener Pfad
  (z. B. ein direktes SQL-Update, das den Trigger umgeht) wäre ein stiller
  Korrektheitsfehler; Testabdeckung muss das absichern.
- `adopt_on_first_sync` ([reconcile.rs:42-44](../../apps/desktop/src-tauri/src/sync/reconcile.rs#L42-L44))
  nutzt denselben Endpoint und muss auf Pagination umgestellt werden.
