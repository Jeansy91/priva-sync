# ADR-0001: Repository-Split nach Aggregaten

**Status:** Accepted
**Datum:** 2026-05-21

## Kontext

`internal/repository/file.go` ist auf 763 Zeilen und 35 Methoden gewachsen
und bündelt fünf unabhängige Aggregate:

- **SyncRoot** — Create/List/Get/Delete/SumSizes (+ Sidecar-Pflege)
- **File** (eng) — Upsert, FindByPath, ListDirectory/Subtree, Move
- **Trash** — SoftDelete, MarkTrashed/Deleted, ListTrash, Restore, Purge
- **Conflict** — Create, ListOpen, Resolve (+`Tx`-Variante)
- **Directory** — CreateDirectory (Marker-Row für leere Ordner)

Der Cut nach Aggregat ist klar: Methoden teilen sich Tabellen-Foki
(`conflicts`, `files`-mit-`trashed=true`, `sync_roots`), aber nicht
Geschäftsregeln. Trash-Lifecycle hängt nicht vom Conflict-Lifecycle ab;
SyncRoot-Sidecar-Pflege hat nichts mit Move-Versionschecks zu tun.

Heute scannt man 763 Zeilen, um ein Aggregat zu verstehen. Tests sind
breit (jeder Test braucht die gesamte `FileRepository`-Initialisierung).
Caller-Konstruktoren in Handlern und Coordinator nehmen eine
universelle `*FileRepository`, obwohl die meisten nur ein Aggregat
brauchen.

## Entscheidung

`FileRepository` wird nach Aggregat aufgespalten:

| Repository | Verantwortung | Tabelle(n) |
|---|---|---|
| `SyncRootRepository` | Lifecycle + Sidecar-Pflege | `sync_roots` |
| `FileRepository` (eng) | Datei-CRUD, Move, Listing | `files` (aktiv) |
| `TrashRepository` | Soft-Delete, Restore, Purge | `files` (`trashed=true`) |
| `ConflictRepository` | Konflikt-Lifecycle | `conflicts` |

Aggregate teilen sich `pgxpool.Pool` und das `storage.Service`-Handle, wo
Sidecars beteiligt sind. Cross-Aggregat-Schreibvorgänge laufen über
`*Tx`-Methoden in einer vom Aufrufer geöffneten Transaktion (Muster
existiert bereits: `DeleteSyncRootTx`, `ResolveConflictTx`).

### Phasen

1. **Conflict abspalten** (jetzt) — kleinster, in sich geschlossener Cut.
   4 Methoden, 5 Caller-Stellen. Setzt Muster für Folge-Phasen.
2. **Trash abspalten** (Folge-Issue) — 8 Methoden, mehr Caller (Trash-Handler,
   Cron-Purger, Coordinator). Tx-Verkehr mit `files` über `PurgeTrashRecordTx`.
3. **SyncRoot abspalten** (Folge-Issue) — Sidecar-Pflege wandert mit; `DeleteSyncRootTx`
   bleibt cross-Aggregat (löscht Files-Rows).
4. **FileRepository (eng) bleibt** als Rest.

## Konsequenzen

**Positiv:**
- Lokalität: Aggregat-Logik an einer Stelle, Caller fordern explizit was
  sie brauchen (`*ConflictRepository` statt opaker `*FileRepository`).
- Tests fokussiert: Conflict-Tests brauchen nicht die Trash-/SyncRoot-Welt.
- Naht für künftige Strategiewechsel (z.B. Conflict-Store auf separates
  Backend) wird sichtbar statt vergraben.

**Negativ:**
- Mehr Konstruktor-Felder in Coordinator und FileHandler. Wird durch
  expliziten Aggregat-Bezug bezahlt.
- Cross-Aggregat-Operationen brauchen weiter `*Tx`-Methoden + UoW im
  Aufrufer. Kein Regress — Muster ist bereits etabliert.

## Status der Phasen

- Phase 1 (Conflict): umgesetzt.
- Phase 2 (Trash): umgesetzt.
- Phase 3 (SyncRoot): umgesetzt.
- Phase 4 (FileRepository eng): natürlich erreicht — nach Phasen 1-3
  bleiben 9 zusammenhängende Methoden (Upsert, CreateDirectory,
  UpsertWithVersionCheck, FindByPath, ListSubtree, ListDirectory,
  GetChangedSince, MoveFile, MoveFileWithVersionCheck). Kein
  Storage-Handle mehr nötig — `FileRepository` hält nur noch den Pool.
