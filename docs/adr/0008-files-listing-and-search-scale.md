# ADR-0008: Files-Listing und Suche auf Million-File-Roots

**Status:** Accepted
**Datum:** 2026-05-21

## Kontext

Ein Sync-Root kann produktiv >1 Mio Dateien enthalten (das abzulösende
Bestandsprodukt belegt diese Größenordnung bereits heute).

Bisheriger Stand:

- `FileRepository.ListDirectory` liefert den **gesamten Subtree** unter
  `prefix` per `path LIKE prefix%` zurück.
- Das Frontend ruft `apiClient.files.list(rootID, path)` beim Wechsel in
  jeden Ordner und hält das vollständige Ergebnis in `allFiles` —
  `buildEntries` aggregiert daraus die aktuell sichtbare Ebene.
- Die Suche filtert clientseitig über `allFiles` (siehe
  [`searchEntries.ts`](../../apps/web/src/pages/files/searchEntries.ts)).

Für kleine Roots funktioniert das. Bei einem Root mit Millionen Einträgen
kollabiert das Modell: Der erste Klick auf den Root-Knoten zieht potenziell
Hunderte MB JSON, das Browser-RAM wächst ins GB, und jede Navigation
wiederholt das. Die clientseitige Suche erbt dieses Problem 1:1.

Es gibt keinen `pg_trgm`-Index und keinen dedizierten Such-Endpoint. Das
Schema bietet `is_deleted` zum Trash-Ausschluss, `is_dir` zur Typisierung
und einen `UNIQUE (sync_root_id, path)`-Index.

## Entscheidung

Listing und Suche werden **gemeinsam** auf das Million-File-Szenario
umgestellt, in dieser Reihenfolge:

### 1. Listing — lazy pro Ebene

- `ListDirectory(rootID, prefix)` liefert nur **eine** Pfadebene direkt
  unter `prefix`, nicht den Subtree.
- SQL-Variante: `WHERE path LIKE 'prefix%' AND path NOT LIKE 'prefix%/%/%'`
  (oder generierter `depth`-Column). Der bestehende
  `UNIQUE (sync_root_id, path)`-Index trägt den Lookup.
- Frontend-Effekt: `buildEntries` arbeitet nur noch auf der aktuellen
  Ebene; rekursive Operationen (z.B. Löschen ganzer Verzeichnisse,
  ZIP-Download eines Subtrees, Dir-Statistik im Detail-Panel) erhalten
  dedizierte serverseitige Endpunkte und nutzen kein eager-Listing mehr.
- Die clientseitige Suche entfällt.

### 2. Suche — serverseitig mit Trigram-Index

- Neue Migration aktiviert `pg_trgm` und legt einen partiellen GIN-Index:

  ```sql
  CREATE EXTENSION IF NOT EXISTS pg_trgm;
  CREATE INDEX idx_files_path_trgm
    ON files USING gin (lower(path) gin_trgm_ops)
    WHERE is_deleted = FALSE;
  ```

- Endpoint: `GET /sync-roots/{id}/files/search?q=...&path=/sub&limit=200&cursor=...`.
- Query-Skelett:

  ```sql
  SELECT ...
    FROM files
   WHERE sync_root_id = $1
     AND is_deleted   = FALSE
     AND path LIKE $2 || '%'        -- Scope: aktueller path
     AND lower(path) LIKE $3        -- Trigram-beschleunigt
   ORDER BY path
   LIMIT $4;
  ```

- Wildcards: `*` aus der Query wird zu `%` übersetzt; `%` und `_` aus dem
  User-Input werden escaped. Andere Regex-Metazeichen bleiben literal.
- Pagination: Keyset auf `path` (eindeutig pro Root). Cursor = letzter
  Pfad der vorigen Seite.
- Treffer enthalten ganze Pfade — das Frontend leitet die Dir-vs-File-
  Anzeige aus `is_dir` ab. Da auch Verzeichnis-Segmente in `path`
  enthalten sind, matcht die Suche implizit auch Ordnernamen ohne
  zusätzliche Query.
- Trash bleibt aus der Suche ausgeschlossen. Eine eigene Trash-Suche
  kann später als separater Endpoint folgen.

## Konsequenzen

**Positiv:**

- Roots mit Millionen Dateien werden navigierbar — RAM-Bedarf entkoppelt
  von Root-Größe.
- Suche skaliert mit dem Trigram-Index statt mit der Datei-Anzahl im
  Browser.
- Konsistentes Modell: weder Listing noch Suche laden mehr ganze
  Subtrees.

**Negativ:**

- Bestehende rekursive Code-Pfade (Recursive-Delete in
  `FilesPage.handleDelete`, ZIP-Download, evtl. weitere) müssen auf
  serverseitige Operationen umgestellt werden, weil sie heute auf
  `allFiles` mit ganzem Subtree zugreifen.
- pg_trgm GIN-Index kostet zusätzlichen Speicher und verlangsamt INSERTs
  marginal — bei Sync-Last vermutlich vernachlässigbar, aber zu
  messen.
- API-Bruch in `files.list`: ältere Clients (Desktop-Sync) müssen
  geprüft werden, ob sie das eager-rekursive Verhalten irgendwo
  voraussetzen.
- Such-UI wird komplexer (Loading-State, Pagination, „mehr laden", Fehler-
  zustand) als die jetzige In-Memory-Filterung.
