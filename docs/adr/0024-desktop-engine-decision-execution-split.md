# ADR-0024: Desktop-Sync-Engine — Entscheidungslogik pur, Ausführung getrennt

**Status:** Accepted
**Datum:** 2026-07-09

## Kontext

Die Rust-Sync-Engine des Desktop-Clients testet ihre puren Helfer
gründlich (`predicates.rs`, `paths.rs`, `ignore.rs`, `delta.rs`,
`retry.rs`, `conflict_resolve.rs`), aber die eigentliche
Entscheidungslogik ist ungetestet: `apply_server_change`
(`sync/reconcile.rs`, ~230 Zeilen Branch-Matrix über
deleted/dir/conflict/readonly-Mount/placeholder) und `run_uploads`
(`sync/transfer.rs`) sind an DB, Dateisystem, Tauri-`AppHandle` und
HTTP-Client geschweißt und nur im Livebetrieb ausführbar. Die Bugs, die
Sync-Korrektheit gefährden, leben genau in diesen Aufrufern — die puren
Extraktionen haben Testbarkeit ohne Locality gebracht.

Zusätzlich ist Ausführungs-Choreografie kopiert: der
Conflict-Kopie-Ablauf (`make_conflict_path` → Kopie → Server-Bytes →
`set_conflict` → `sync_conflict`-Emit) existiert doppelt (Upload- und
Download-Pfad), der Failure-Record-Block (Timestamp →
`compute_next_retry` → `set_failed` → `warn!` → `sync_failure`-Emit)
viermal.

## Entscheidung

Entscheidung und Ausführung werden getrennt; das Interface der
Entscheidung ist die Test-Oberfläche:

- Pro Engine-Pfad eine **pure Entscheidungs-Funktion**:
  `(server_entry, local_state) → ServerChangeAction` für den
  Download-/Reconcile-Pfad, analog ein Action-Enum für den Upload-Pfad.
  Die Enums benennen jede Verzweigung der heutigen Matrix explizit
  (Skip, CreateDir, Download, DeleteLocal, ConflictCopy{…}, …).
- Ein **Executor** pro Pfad führt Actions aus (FS-Operationen,
  DB-Schreiben, Emits) und trifft selbst keine Entscheidungen.
- Zwei gemeinsame Executor-Helfer geben kopierter Choreografie ein
  Zuhause: `record_failure(…)` (Backoff-Policy + `sync_failure`-Emit,
  einmal statt viermal) und `materialise_conflict(…)`
  (Conflict-Kopie + `set_conflict` + `sync_conflict`-Emit, einmal statt
  zweimal).
- Die Branch-Matrix wird als Tabellen-Tests über die
  Entscheidungs-Funktionen abgedeckt — gleiches Muster wie die bereits
  getesteten puren Module.

Der Umbau ist pro Branch slicebar; Ziel-Endzustand ist, dass
`apply_server_change` und `run_uploads` nur noch
Entscheiden-dann-Ausführen orchestrieren.

## Konsequenzen

**Positiv:**

- Sync-Korrektheit (Kern des Produkts, ADR-0006) wird als pure
  Branch-Matrix testbar — ohne DB, FS oder AppHandle.
- Retry- und Conflict-Policy haben je genau ein Zuhause; Fixes landen
  nicht mehr ungleichmäßig in Kopien.
- Neue Sync-Fälle (z. B. künftige Mount-Sonderfälle) erweitern ein Enum
  und werden vom Compiler auf Vollständigkeit geprüft.

**Negativ:**

- Großer Umbau in den heißesten Dateien der Engine; muss in Slices mit
  unverändertem Verhalten erfolgen.
- Die Action-Enums müssen genug Kontext tragen, damit der Executor dumm
  bleiben kann — zu magere Enums drücken Entscheidungen zurück in den
  Executor.
