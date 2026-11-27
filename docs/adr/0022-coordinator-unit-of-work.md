# ADR-0022: Coordinator-Mutation als Unit-of-Work — TX-Runner, Outbox-Batch, ein Conflict-Materialisierer

**Status:** Accepted
**Datum:** 2026-07-09

## Kontext

Jede der ~12 Methoden des **Sync Coordinator** baut dasselbe mechanische
Skelett von Hand: `pool.Begin` → `defer tx.Rollback` → Repo-Operation →
mehrere `outbox.Insert(ctx, tx, group, <seq>, …)` mit handgetippten
Sequenznummern → `bus.PublishCritical` → `tx.Commit` → `worker.Wake()`.
Die Seq-Nummern (Ordering pro `group_id`: rename → sse → mail) stehen als
Literale (`1,2,3,4`) bzw. als manuelles `seq++` im Code — ein Off-by-one
ist durch nichts prüfbar außer der 1899-zeiligen Pipeline-Testdatei.

Zusätzlich existiert die **Conflict**-Materialisierung dreifach fast
wortgleich: `realConflict` (Upload), `realMoveConflict` (Move),
`realRestoreConflict` (Restore) — jeweils `buildConflictPath` →
Conflict-Row → Outbox-Copy/Rename → SSE-Payload → `FileConflictKept` +
`ConflictCreated`. Es variiert nur die Storage-Operation (Copy vs. Rename)
und welches `File` zurückgegeben wird.

ADR-0017 verwarf ein generisches `MutationTx`-Template, das die pro
Mutation **absichtlich verschiedene** Outbox-/Bus-Choreografie auf einen
kleinsten gemeinsamen Nenner gezwungen hätte. Diese Ablehnung bleibt
gültig — die vorliegende Entscheidung betrifft ausschließlich den
mechanischen Rahmen (TX-Lebenszyklus, Seq-Vergabe) und eine konkrete,
dreifach kopierte Choreografie. Sie schablonisiert nicht, *welche* Events
und Outbox-Rows eine Mutation schreibt.

## Entscheidung

Drei Vertiefungen **innerhalb** des Coordinators, sein öffentliches
Interface bleibt unverändert:

- **TX-Runner** `withMutationTx(ctx, fn)`: besitzt Begin, Rollback-defer,
  Commit und `worker.Wake()`. Die Closure erhält `tx` und schreibt frei
  ihre Choreografie. Kein Verhaltens-Template — nur Lebenszyklus.
- **Outbox-Batch**: ein pro TX erzeugter Batch nummeriert `seq` monoton
  selbst (`batch.Add(kind, payload)`); die Aufruf-Reihenfolge ist die
  Ausführungs-Reihenfolge. Handgetippte Sequenznummern verschwinden.
- **Ein Conflict-Materialisierer** `materialiseConflictCopy(…)`,
  parametrisiert über die Storage-Operation (Copy vs. Rename) und die
  Rückgabe-Datei. `realConflict`, `realMoveConflict` und
  `realRestoreConflict` werden zu dünnen Aufrufern.

Grenze zu ADR-0017: sobald eine Mutation vom Runner mehr verlangt als
Lebenszyklus + Seq-Vergabe, gehört die Logik in die Mutations-Methode,
nicht in den Runner.

## Konsequenzen

**Positiv:**

- Seq-Ordnung ist konstruktionsbedingt korrekt statt handgezählt —
  die Fehlerklasse Off-by-one entfällt (Locality der Ordering-Regel).
- Der Conflict-Ablauf hat ein Zuhause; eine Änderung (z. B. neues Event)
  landet einmal statt dreimal.
- Unterhalb der Pipeline-Tests entsteht ein Seam: Batch-Ordnung und
  Conflict-Materialisierung sind einzeln testbar; `coordinator_test.go`
  bleibt als End-to-End-Netz bestehen.

**Negativ:**

- Eine Indirektion mehr; Leser müssen `withMutationTx` kennen, bevor sie
  eine Mutations-Methode verstehen.
- Die ADR-0017-Grenze muss aktiv gehalten werden — der Runner darf nicht
  schleichend Choreografie anziehen.
