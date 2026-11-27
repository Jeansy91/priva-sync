# ADR-0001: Dateisystem-Resilienz via JSON-Sidecars

**Status:** Accepted
**Datum:** 2026-04-30

## Kontext

PostgreSQL hält den Datei-Index (Metadaten, Hash, Versions-Token, Quota,
Shares). Bei DB-Totalverlust (korruptes Volume, fehlerhafte Migration,
versehentliches `DROP`) wäre der State weg, obwohl die eigentlichen Bytes
auf `/data` unbeschädigt liegen.

Ein reines Backup der DB schützt nicht vor Korruptions-Fenstern und macht
den Operator-Workflow komplizierter. Self-Hoster wollen "Daten sind im
Dateisystem — der Rest baut sich neu auf".

## Entscheidung

Die DB ist ein **rekonstruierbarer Cache**. Alle für die Wiederherstellung
notwendigen Metadaten liegen redundant am Dateisystem als **JSON-Sidecars**:

| Pfad                             | Inhalt                                                                       |
| -------------------------------- | ---------------------------------------------------------------------------- |
| `/data/users/{id}.json`          | Alle `users`-Spalten (Password-Hash, TOTP, Rolle, Quota, Sprache, Settings) |
| `/data/sync_roots/{id}.json`     | `id`, `user_id`, `name`, `path`, `created_at`                                |
| `/data/shares/{id}.json`         | `id`, `user_id`, `sync_root_id`, `path`, `share_type`, `token`, `expires_at`, `can_write` |

Sidecars werden bei Create/Update atomar geschrieben (`.tmp` + `os.Rename`)
und bei Delete entfernt. Die Datei-Tabelle (`files`) braucht keine
Sidecars — sie wird beim Recovery durch Traversierung des `/data`-Baums
neu aufgebaut, Hashes werden re-computed (BLAKE3 über den Datei-Inhalt).

**`cmd/recover`** liest:
1. Migrationen ausführen (Schema falls fehlt).
2. `/data/users/*.json` → `users`-Tabelle (`INSERT … ON CONFLICT DO NOTHING`).
3. `/data/sync_roots/*.json` → `sync_roots`-Tabelle.
4. `/data/shares/*.json` → `shares`-Tabelle.
5. Per Sync-Root den Dateibaum traversieren, BLAKE3 berechnen, `files`-Rows
   einfügen (live + Trash).

## Konsequenzen

**Positiv:**
- DB-Verlust ist non-fatal — `recover` baut alles bis auf flüchtigen State neu.
- Sidecars sind portabel: tar/zip eines `/data`-Mounts hat alles dabei.
- Keine FS-Kompatibilitäts-Annahmen — funktioniert auf jedem POSIX/NTFS-FS,
  inkl. FAT32 und Docker-Volume-Treibern, die Extended Attributes nicht
  unterstützen.

**Negativ:**
- Recovery muss alle Dateien erneut hashen — bei Millionen von Dateien
  spürbar (BLAKE3 mildert es, aber I/O bleibt das Bottleneck).
- Pro entitäts-mutierender Operation eine zusätzliche Sidecar-Schreibung.
- Refresh-Tokens, Audit-Log, Konflikt-Records und exakte `used_bytes` werden
  **nicht** rekonstruiert (sicherheitskritisch bzw. neu ableitbar via
  `pg_sum(files.size)`).
