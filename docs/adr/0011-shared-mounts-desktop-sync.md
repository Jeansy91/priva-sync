# ADR-0011: Shared Mounts — Desktop-Sync

**Status:** Implemented (Grill-Review 2026-05-31 gegen Desktop-Code; ersetzt die
Proposed-Skizze. Umgesetzt in 11 Slices auf `feat/adr-0011-shared-mounts-desktop`
am 2026-06-01. Korrekturen ggü. Erst-Entwurf siehe „Revisionen" unten.)
**Datum:** 2026-05-27, revidiert + umgesetzt 2026-05-31..2026-06-01

## Kontext

[ADR-0010](0010-shared-mounts-recipient-view.md) führt **Shared Mounts** als
empfängerseitige Sicht auf eingehende `user`-Shares ein, klammert die
Desktop-Sicht aber aus. Dieses ADR füllt die Lücke.

Bestehender Desktop-Stand (verifiziert im Code, nicht im Erst-Entwurf
berücksichtigt):

- `SyncRootEntry` ist **flach** (`apps/desktop/.../db/mod.rs`): `local_path`,
  `server_root_id`, `download_mode`, `read_only`, `paused`. Es gibt **keinen**
  Union-Typ und keine Mount-Awareness.
- `read_only` existiert seit DB-Migration 3, wird aber **nirgends gesetzt
  oder abgefragt** — totes Feld, ideal für Mounts.
- URL-Konstruktion ist hart `/sync-roots/{id}/...`, dupliziert in
  `ApiClient` + `ApiSnapshot` (`api/files.rs`).
- SSE (`sse.rs`) kennt nur `conflict_added`/`conflict_resolved`/
  `file_uploaded` — **nicht** `ShareAdded`/`ShareRevoked`.
- `prune_orphaned_sync_roots` (`sync/engine_loop.rs`) vergleicht lokale Roots
  gegen `GET /sync-roots` und räumt fehlende lokal auf (Bytes bleiben, CF
  unregister, Notification) — das **ist** bereits die Widerruf-Mechanik.
- Eine **komplette Retry-Maschinerie** existiert (`db/failed_changes.rs`,
  `sync/retry.rs`): `sync_status`, `attempt_count`, `next_retry_at`,
  exponentielles Backoff.

Aus ADR-0010 fest: Quota/Trash laufen auf den Geber; Re-Sharing verboten;
bei Widerruf bleiben Bobs lokale Bytes.

## Entscheidung

### Datenmodell — `kind`-Flag statt Union (Revision 1)

Kein `Root::Owned|Mount`-Union. Stattdessen `sync_roots` um
`kind TEXT NOT NULL DEFAULT 'owned'` (`owned`|`mount`). Bei `kind='mount'`
trägt `server_root_id` die **share_id**. Die gesamte Engine-Mechanik
(Watcher, Reconcile, Upload/Download-Queue, CF-Provider, Prune) bleibt
**unverändert** — nur zwei Stellen verzweigen:

1. URL-Konstruktion (`api/files.rs`): `base_path(kind, id)` →
   `/sync-roots/{id}` oder `/shared-mounts/{share_id}`.
2. `prune_orphaned_sync_roots`: `kind='mount'`-Roots gegen `GET /shared-mounts`
   prüfen, eigene wie bisher gegen `GET /sync-roots`.

Preis: `SyncRootEntry` leicht überladen (ein Feld nur für Mounts relevant).
Akzeptiert, weil die Union die Engine-Loop verdoppeln würde.

### Onboarding — Server-Pull autoritativ, SSE nur Latenz (Revision 2)

- Neuer API-Call `list_shared_mounts()` → `GET /api/v1/shared-mounts`.
- Der Sync-Loop pullt pro Cycle **beide** Quellen. **Aktivierte Mounts**
  (lokaler Pfad gewählt) liegen als `sync_roots`-Zeile (`kind='mount'`) und
  werden gesynct. **Verfügbare Mounts** (kein Pfad) leben **nur abgeleitet
  aus dem Pull**, nicht lokal persistiert, und werden im Tray als
  „verfügbar, nicht aktiviert" gezeigt — **nie auto-aktiviert**.
- `ShareAdded`/`ShareRevoked` werden im Desktop-SSE ergänzt, triggern aber
  **nur** einen sofortigen Pull-Refresh. SSE ist reine Latenz-Optimierung;
  der Pull ist die Wahrheit (robust gegen verpasste Events).

### Permissions — Engine-Guard autoritativ, RO pro Root (Revision 3)

`can_write` ist eine Eigenschaft der Share-Row → **pro Mount**, nie pro
Datei. Durchsetzung doppelt, aber der Engine-Guard ist die Garantie:

1. **Harte Stage-Regel:** `if root.read_only { keine Upload/Move/Delete-
   Staging }`. Ein RO-Mount produziert grundsätzlich **keine** Pending-
   Mutation — das verhindert den 403-Endlos-Retry an der Wurzel.
2. RO-OS-Attribut nur als UX-Hinweis (Explorer-Schloss, Editor warnt früh).
3. Lokaler Edit auf RO-Mount: nächster Reconcile **überschreibt mit
   Server-Version** (Download gewinnt) **plus Toast** „Änderung an X
   verworfen — kein Schreibrecht". Bobs Edit geht verloren, korrekt da
   kein Schreibrecht; der Toast verhindert spurlosen Datenverlust.

### Quota-voll — eigener Status, geliehene Retry-Felder (Revision 4)

507/Quota-Code → neuer `sync_status = 'blocked_remote_quota'`, der die
bestehenden Retry-Felder re-nutzt, aber mit **fixem 60s-Tick** statt
exponentiellem Backoff. Eigene Notification mit Geber-Bezug („Alice hat
keinen Speicherplatz — N Dateien warten"). Lokale Datei bleibt, kein
Rollback. **Poll-only, kein SSE** — `QuotaThresholdCrossed` geht an Alice;
Bob darf Alices Füllstand nicht mitlesen (Privacy). Alle anderen Non-2xx →
wie gehabt `pending_failed`.

### Konflikte (Revision 5)

- Loser-Kopie liegt in Alices Root, innerhalb `share.path` → **kommt über
  den normalen Download-Pfad** in Bobs Mount. Desktop tut hierfür nichts.
- `on_conflict_added` zeigt Notification **nur**, wenn die root/share-ID zu
  einem **lokal aktivierten** Root/Mount gehört — sonst ignorieren.
- Die Web-`/conflicts`-Erweiterung (Mount-Konflikte listen + auflösen) wird
  gebaut, aber als **eigener Slice** (Server-Query-Union + Web-UI), getrennt
  vom Desktop-Teil testbar.

### Mount entfernen / Widerruf (Revision 6)

- **Widerruf/Ablauf/Root-Löschung** sehen für Bob identisch aus: Share fällt
  aus `GET /shared-mounts` → bestehender Prune-Pfad (Bytes bleiben, CF weg,
  Notify). `ShareRevoked`-SSE = nur sofortiger Prune statt Poll-Wartezeit.
- **Mount entfernen** (Bob-lokal): neuer Tauri-Command `remove_mount(share_id)`
  → `remove_sync_root` (CF unregister, DB-Zeile weg, Bytes bleiben),
  **Server unangetastet**. Der Mount erscheint sofort wieder als
  „verfügbar" (kein `dismissed`-State) — er *ist* serverseitig verfügbar.

### Datei-Mounts — web-only (Revision 7)

`is_file=true`-Shares **ignoriert der Desktop**; der Pull filtert sie raus,
sie erscheinen nicht in der Tray-Liste. Engine bleibt rein
verzeichnisbasiert (kein Einzeldatei-Watcher/Placeholder-Edge-Case).
Bewusste Scope-Grenze ggü. der Web-Sicht.

### Auth + If-Match (Revision 8)

- Mount-Schreibvorgänge nutzen **denselben** Bearer wie eigene Roots (Bobs
  Token); nur die URL ändert sich. Server bucht via `ActorUserID =
  claims.UserID` auf Alices Quota + schreibt Bob als `last_modifier`
  (ADR-0013, bereits implementiert). Keine Auth-Änderung nötig.
- **Server-Vorbedingung (Bug):** `shared_mounts_mutate.go` (201 Z.) enthält
  **0× `IfMatch`** — der Mount-Handler parst/leitet `If-Match` nicht weiter,
  während der Eigen-Root-Handler `parseIfMatch(r)` nutzt. Folge: Mount-
  Konflikte laufen **immer** über die Loser-Kopie, nie über sauberen 409 —
  abweichend von eigenen Roots. Der Desktop sendet `If-Match` bereits.
  **Erster Slice ist daher ein Server-Fix** (`If-Match` im Mount-Handler an
  den Coordinator durchreichen) + Handler-Test analog Eigen-Root.

## Konsequenzen

**Positiv**
- Desktop-Mount-Sync re-nutzt ~90% der Engine; neue Abzweige sind nur
  URL-Konstruktion, RO-Stage-Guard und `blocked_remote_quota`.
- Pull-autoritativ + SSE-als-Latenz ist robust gegen verpasste Events.
- „Entfernen" und „Widerruf" enden lokal gleich — einfach erklär- und
  testbar.

**Negativ / offen**
- `SyncRootEntry` trägt ein Mount-only-Feld (`kind`/überladenes
  `server_root_id`).
- `can_write`-Updates (Permission-Wechsel ohne Widerruf+Neuanlage) brauchen
  ein neues SSE-Event + Update-API — **nicht** Teil dieser ADR.
- CF-Provider-Limit unter Windows bei vielen Mounts unbekannt — vorerst
  nicht-bindend, produktiv beobachten.

## Revisionen ggü. Erst-Entwurf (2026-05-31)

1. Union-Typ → `kind`-Flag (Code hat flache `SyncRootEntry`).
2. SSE-primär → Pull-primär (Desktop-SSE kennt `ShareAdded` nicht).
3. RO pro-Datei → Engine-Guard pro-Root + Toast bei Verwurf.
4. Quota-voll: eigener Status `blocked_remote_quota`, fixer 60s-Tick.
5. Konflikte: Desktop minimal; Web-Liste als eigener Slice.
6. Prune-Pfad existiert bereits — Widerruf re-nutzt ihn.
7. Datei-Mounts web-only (Desktop ignoriert `is_file`).
8. Server-Bug entdeckt: Mount-Handler ignoriert `If-Match` → Server-Fix
   als erster Slice.
