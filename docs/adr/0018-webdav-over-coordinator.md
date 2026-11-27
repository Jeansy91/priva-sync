# ADR-0018: WebDAV als FileSystem-Adapter über dem Sync Coordinator

**Status:** Accepted
**Datum:** 2026-06-20

## Kontext

Gefordert ist ein vollständiger WebDAV-Endpoint (RFC 4918, Port 8081),
kompatibel mit Rclone, Windows-Netzlaufwerk und macOS Finder. Der bestehende
[`webdav/handler.go`](../../apps/server/internal/webdav/handler.go) montiert
dafür `golang.org/x/net/webdav` mit `gwebdav.Dir({dataPath}/{userID})` direkt
auf den Roh-Dateibaum.

Dieser Direkt-FS-Mount **umgeht die gesamte Architektur**. Single Source of
Truth für File-State ist die `files`-Tabelle (siehe `docs/glossary.md`), nicht das
Dateisystem. Der Mount erzeugt drei harte Defekte:

1. **Geister-Dateien.** Ein WebDAV-PUT schreibt rohe Bytes nach
   `{dataPath}/{userID}/{syncRootID}/...`, legt aber **keine `files`-Row** an.
   Die Datei taucht nie im Web-UI auf, löst kein Delta-Sync aus und wird erst
   bei einem Offline-`cmd/recover`-Rebuild sichtbar.
2. **Quota-Umgehung.** Kein `quota.CheckHeadroom` → ein WebDAV-Client kann das
   **Kontingent** beliebig überschreiben; `UsedBytes` bleibt falsch.
3. **Keine Side-Effects.** Kein SSE, keine Conflict-Row, kein
   `last_modifier_user_id`, keine Outbox — alles, was der **Sync Coordinator**
   transaktional garantiert, fehlt.

Zusätzlich exponiert der Mount-Root `{dataPath}/{userID}` die internen
Sync-Root-**UUIDs** als Top-Level-Ordner sowie `.chunks`/`.trash` — ein
Layout, das kein Nutzer erwartet.

Der Coordinator ist bereits der tiefe, saubere Kern für jede Mutation
(Authorize → Quota-Gate → DB-TX → Outbox → Commit). Die REST-Handler sind
dünne Adapter darüber (ADR-0017). WebDAV darf kein zweiter, paralleler
Schreibpfad am Coordinator vorbei sein.

## Entscheidung

WebDAV wird ein **Adapter über demselben Coordinator** wie REST. Statt
`gwebdav.Dir` implementieren wir eine eigene `webdav.FileSystem`, die Reads aus
dem File-Repository und **jede Mutation durch den Coordinator** bedient.

### Namensraum

- `/` listet die **Sync Roots des Users nach Namen** (nicht nach UUID).
- `/<RootName>/<pfad...>` löst `RootName` → `SyncRootID` auf; der Rest ist der
  Pfad innerhalb des Roots. `.trash`/`.chunks` sind unsichtbar.
- Kollidierende Root-Namen werden deterministisch disambiguiert (Suffix);
  Detail im Slice, nicht hier.

### Operationen-Mapping

| WebDAV         | Backend |
|----------------|---------|
| PROPFIND/GET/Stat/Readdir | `FileRepository` (Read) + `storage.ServeFile` (Bytes) |
| PUT            | Staging-Write → `coordinator.UploadFile` |
| MKCOL          | `files.CreateDirectory` + `storage.MakeDir` (wie der REST-Directory-Handler — Directory-Creation ist bewusst **kein** Coordinator-Vertical) |
| DELETE         | `coordinator.DeleteFile` / `DeleteSubtree` |
| MOVE           | `coordinator.MoveFile` |
| COPY           | fällt über `OpenFile`(read)+`OpenFile`(write) auf den PUT-Pfad zurück |
| LOCK/UNLOCK    | `gwebdav.NewMemLS()` (unverändert) |

Der Coordinator bleibt **unangetastet** — kein neues Vertical, keine neue
Cmd-Variante. WebDAV ist reiner Adapter (Linie ADR-0017).

### PUT-Overwrite-Semantik

RFC 4918 PUT ist Last-Writer-Wins; WebDAV-Clients senden im Normalfall kein
`If-Match`. Der Coordinator würde ein `IfMatch == nil` auf einen existierenden
Pfad aber als **echten Conflict** behandeln (Loser-Copy) — falsch für WebDAV.

Lösung ohne Coordinator-Eingriff: Der Adapter liest vor dem Write die aktuelle
`server_version` (`FindByPath`) und übergibt sie als `IfMatch`. Damit nimmt der
Coordinator den sauberen Overwrite-Pfad. Bei Versions-Race (paralleler Writer
zwischen Read und Write) → `ConflictVersionMismatch`; der Adapter liest neu und
wiederholt **gebounded** (Last-Writer-Wins). Clients, die explizit
`If`/`If-Match` (RFC 7232) senden, bekommen später echte optimistische
Concurrency — als Folge-Slice, nicht in diesem ADR.

### Auth

Basic Auth + Bearer bleiben wie im bestehenden Handler (Rclone/Finder ↔ Basic,
Desktop ↔ Bearer). Die Auth-Logik ist unverändert korrekt und wird nur in den
neuen Handler übernommen.

## Verworfene Alternativen

- **`gwebdav.Dir` behalten + Recovery-Reconciler live machen.** Würde einen
  zweiten, eventuell-konsistenten Schreibpfad zementieren; Quota/SSE blieben
  zeitversetzt falsch. Tech-Schuld statt Tiefe.
- **Direkt-FS als bewusste „Raw-Power-User-Sicht".** Widerspricht „DB ist
  Source of Truth"; Web und WebDAV zeigten divergierende Bäume. Verworfen.
- **„Force-Overwrite"-Flag im `UploadCmd`.** Biegt den Coordinator für einen
  Adapter-Spezialfall; das Read-Version-als-IfMatch-Muster erreicht dasselbe
  ohne Eingriff in den tiefen Kern (ADR-0017-Linie).

## Konsequenzen

**Positiv:**
- WebDAV-Schreibvorgänge sind erstklassige Mutationen: Quota-gated, in der
  `files`-Tabelle sichtbar, mit SSE/Conflict/`last_modifier`/Outbox — identisch
  zu REST.
- Ein einziger Schreibpfad (Coordinator) für REST und WebDAV; keine Drift.
- Top-Level zeigt Sync-Root-Namen — erwartbares Mount-Layout für Finder/Rclone.

**Negativ:**
- Die `webdav.FileSystem`-Impl ist nennenswert Code (Pfad-Resolver,
  Write-Buffering-File, Readdir-Mapping) statt eines Einzeilers `gwebdav.Dir`.
- TOCTOU-Fenster im PUT-Overwrite (Read-Version → Write); via gebounded Retry
  als Last-Writer-Wins aufgelöst, nicht streng serialisiert.
- MKCOL umgeht den Coordinator (wie der REST-Directory-Handler) — bewusst
  konsistent, aber Directory-Creation bleibt ein vom Coordinator getrennter
  Pfad.
