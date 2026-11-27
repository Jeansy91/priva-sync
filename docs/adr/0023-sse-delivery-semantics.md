# ADR-0023: SSE-Liefersemantik explizit — Durable via Outbox, Ephemeral via Hub

**Status:** Accepted
**Datum:** 2026-07-09

## Kontext

SSE-Events erreichen Clients über zwei Wege: Coordinator-Mutationen
schreiben sie transaktional in die **Outbox** (crash-sicher, committet mit
der Daten-Row; `internal/sync/sse_typed.go`), Handler-seitige Aktionen
publizieren direkt an den Hub (best-effort, bei Crash verloren;
`internal/api/handler/sse_publish.go`). Beide Wege sind legitim — aber
nichts auf Typ-Ebene unterscheidet sie. Derselbe Event-Typ (z. B.
`FileAdded`) läuft mal durabel, mal ephemer, je nach Callsite. Wer die
Liefersemantik eines Events verstehen will, springt zwischen
`sse/events`, `sync/sse_typed`, `sse/hub` und `handler/sse_publish`.

## Entscheidung

Ein benannter **EventPublisher**-Seam mit genau zwei Methoden:

- `PublishDurable(tx, event)` — schreibt die Outbox-Row; Zustellung ist
  an den Commit gebunden und crash-recoverbar.
- `PublishEphemeral(event)` — publiziert direkt an den Hub; darf bei
  Crash oder fehlendem Subscriber verloren gehen.

Alle Callsites migrieren auf den Publisher. Direkter Hub-Zugriff und
direkte SSE-Outbox-Inserts außerhalb des Publishers verschwinden aus
Handlern und Coordinator. Die Wahl der Semantik steht damit am Callsite
im Methodennamen; neue Events müssen sich explizit entscheiden.

## Konsequenzen

**Positiv:**

- Liefersemantik ist am Aufruf ablesbar; das Wissen „welcher Kanal wann"
  ist in einem Modul konzentriert (Deletion-Test: Löschen des Publishers
  verstreut genau dieses Wissen wieder über alle Callsites).
- Ein Ort für künftige Delivery-Regeln (Metriken, Backpressure,
  Event-Filterung).

**Negativ:**

- Der Publisher ist eine dünne Schicht — bewusst: Er ist ein benannter
  Seam, kein Verhaltens-Container. Er darf keine Event-spezifische Logik
  anziehen.
- Migration berührt viele Callsites (Conflicts, Shares, Trash,
  Sync-Roots, Shared Mounts, Public Links) — mechanisch, aber breit.
