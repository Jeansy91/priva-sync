# Glossar

Domänenbegriffe von PrivaSync, gruppiert nach Bereich. Wer in Code, Issues, Tests oder
UI-Texten über denselben Sachverhalt spricht, verwendet denselben Begriff. Das Glossar
benennt bewusst auch Wörter, die für einen Begriff **nicht** verwendet werden sollen.

Code-Identifier sind englisch, sichtbare Texte deutsch. Wo beides auseinanderfällt, steht
es beim Begriff (zum Beispiel Quota gegenüber Kontingent).

## Server

**Quota** (UI: Kontingent): Speicher-Budget eines Nutzers. `UsedBytes + TrashBytes` zählen
gegen `QuotaBytes`; der Papierkorb hat kein eigenes Limit (ADR-0004). Nicht: Limit,
Speicherplatz, Disk-Usage.

**Sync Root**: Ein vom Nutzer registrierter Wurzelordner. Jede Datei gehört zu genau einem
Sync Root. Nicht: Folder, Library, Drive.

**Share**: Zeile in `shares`, die einem zweiten Akteur Zugriff auf einen Pfad innerhalb
eines Sync Roots gibt. Zwei Typen: `public` (anonymer Link per Token) und `user` (an einen
konkreten Empfänger). `can_write` regelt Schreibzugriff, `expires_at` ist optional.

**Shared Mount** (Geteilter Einhängepunkt): Sicht des Empfängers auf eine eingehende
User-Freigabe. Erscheint wie ein eigener Wurzelordner, ist technisch aber ein Verweis auf
`(Sync Root des Gebers, Pfad)`. Public-Shares bilden keinen Shared Mount (ADR-0010,
ADR-0012). Nicht: Shared Root, Incoming Share.

**Letzter Bearbeiter** (`last_modifier_user_id`): Nutzer, der die letzte Mutation an einer
Datei ausgeführt hat. Kann vom Eigentümer abweichen, wenn über einen schreibbaren Shared
Mount geschrieben wurde (ADR-0013). Nicht: Editor, Author.

**Conflict**: Konkurrierende Schreibvorgänge auf denselben Pfad. Der Verlierer landet als
`<orig>.conflict-<ts>.<ext>`, eine `conflicts`-Zeile hält den Bezug (ADR-0003). Nicht:
Collision, Mismatch.

**Trash**: Soft-Delete-Bereich. Dateien mit `is_deleted = true` zählen weiter zur Quota,
bis der Papierkorb geleert wird, der Purge-Cron (30 Tage) greift oder der Sync Root
gelöscht wird. Nicht: Recycle Bin.

**Threshold**: 80-%- und 95-%-Marken der Quota. Pro Eskalation in die nächsthöhere Stufe
geht genau eine Mail raus; der Abstieg ist still.

**Quota State**: Enum `ok`, `warning80`, `critical95` anstelle zweier Bool-Flags. Der
Eintritt in `warning80` oder `critical95` publiziert `QuotaThresholdCrossed` und löst die
Mail aus.

**Sync Coordinator**: Dienst, der alle mutierenden Dateioperationen orchestriert. Er besitzt
die Transaktion, prüft die Quota vor dem Staging, unterscheidet Versionskonflikt (409) von
echtem Conflict und schreibt Seiteneffekte in die Outbox (ADR-0022). Nicht: Sync Service,
File Manager.

**Outbox**: Tabelle für transaktional erfasste Seiteneffekte (Storage-Rename, SSE, Mail).
Ein In-Process-Worker arbeitet sie ab; die Reihenfolge pro `group_id` ist Rename, SSE,
Mail. Nicht: Job-Queue, Event-Log.

**Domain Event Bus**: In-Process-Publish/Subscribe für Domänenereignisse. Kritische
Subscriber laufen synchron in der Transaktion, reaktive nach dem Commit. Getrennt von der
Outbox: der Bus dient der Domänenlogik, die Outbox crash-sicheren Seiteneffekten.

**Document Server** (ADR-0027): Extern betriebener, OnlyOffice-API-kompatibler Server für
Office-Vorschau und -Bearbeitung. Eine leere `document_server.url` bedeutet: Feature aus.
Bearbeitbar sind nur Formate mit Round-Trip-Fähigkeit und bei vorhandenem Schreibrecht
(ADR-0033, ADR-0049).

**Media-Stream-Grant** (ADR-0067): Opaker, pfadgebundener, mehrfach nutzbarer Token, mit dem
`<video>` und `<audio>` im Browser Range-fähig streamen, obwohl ein nativer `src` keinen
Authorization-Header senden kann. Nicht verwechseln mit dem einmaligen Download-Token.

**Change-Sequenz** (`files.seq`, ADR-0036): Monoton wachsender, per Trigger gesetzter Zähler
pro `files`-Zeile; einziger Cursor-Typ des Changes-Feed. Nicht: Zeitstempel-Cursor,
`updated_at`.

**Changes-Feed** (`GET /sync-roots/{id}/changes`): Keyset-paginierter Endpoint, über den
Desktop-Clients alle Änderungen seit ihrem Cursor abholen. Seit ADR-0065 mischt er
`files`-Zeilen und Präfix-Move-Events und verlangt `feed_version=2`.

**Präfix-Move-Event** (`move_events`, ADR-0065): Ein einzelner Feed-Eintrag für einen
ganzen Ordner-Move (`old_prefix`, `new_prefix`). Der Client rechnet den Präfixwechsel auf
seine Zeilen selbst um; einzelne Dateien bekommen dabei kein neues `seq`.

**Release** (ADR-0088): GitHub-Release `vX.Y.Z` mit den Client-Assets. Die Client-Version
entspricht immer der Server-Version; der Server verteilt keine Binaries selbst.

**Client-Manifest** (`apps/server/internal/clientrelease/manifest.json`): Zur Build-Zeit von der CI
geschriebene und per `go:embed` ins Server-Binary eingebettete JSON-Datei mit `version`, `pub_date`
und einer `platforms`-Map von Target auf `{asset, signature}`. Im Repo leer. Der Update-Feed baut
daraus die Tauri-Updater-Antwort mit der GitHub-Download-URL.

**Target**: Plattform-Schlüssel im Updater-Format: `windows-x86_64`, `darwin-aarch64`,
`darwin-x86_64`, `linux-x86_64`, `linux-aarch64`.

## Desktop

**Sync Root** (`sync_roots`, `kind='owned'`): Ein lokal gespiegelter eigener Sync Root des
Servers mit `local_path`, `server_root_id`, `download_mode`, `read_only`, `paused`.
Nicht: Ordner, Verzeichnis.

**Shared Mount** (`sync_roots`, `kind='mount'`): Lokale Spiegelung eines eingehenden Shared
Mounts; `server_root_id` trägt hier die `share_id`. Schreibzugriff nur bei `can_write`.
Nur Verzeichnis-Shares; Datei-Mounts ignoriert der Desktop (ADR-0011).

**Aktivierter Mount**: Ein Shared Mount, für den der Nutzer einen lokalen Pfad gewählt hat
und der damit synchronisiert wird.

**Verfügbarer Mount**: Ein auf den Nutzer zeigender Share ohne lokalen Pfad. Wird nur aus
dem Server-Pull abgeleitet, nie lokal persistiert und nie automatisch aktiviert.

**Mount entfernen** (lokal): Vom Nutzer ausgelöst; die `sync_roots`-Zeile verschwindet, die
lokalen Bytes bleiben. Der Mount erscheint sofort wieder als Verfügbarer Mount.

**Widerruf**: Vom Server ausgelöst (Share gelöscht, abgelaufen, Root gelöscht). Lokal
identisch zu Mount entfernen, nur mit anderem Initiator.

**Server-Änderung**: Ein Eintrag aus dem Changes-Feed, den der Client lokal anwendet
(Download, Platzhalter, Löschung). Nicht: Server-Event, Change.

**Offener Server-Änderungs-Auftrag** (ADR-0062): Eine gescheiterte Server-Änderung, die auf
einen neuen Versuch wartet. Er benennt nur den Pfad, nicht den Zustand; beim Retry wird der
aktuelle Serverzustand frisch geholt. Ein eingefrorener Zustand würde eine inzwischen
gelöschte Datei wiederbeleben.

**Verschieben** (lokal erkannt, ADR-0063): Dieselbe Datei liegt danach unter einem anderen
Pfad im selben Sync Root. Erkannt an der Dateiidentität, nicht am Namen. Wird als
Server-Move übertragen, die Server-File-Id bleibt erhalten.

**Dateiidentität**: Vom Betriebssystem vergebene Kennung (NTFS-FileIndex, sonst
inode plus device). Bleibt über ein Verschieben gleich. Nicht: File-Id (das ist die
Server-Id), Inode.

**Verschwunden** (`sync_status`): Der alte Pfad ist weg, ob Verschieben oder Löschung ist
noch offen. Wird zu Beginn des nächsten Sync-Zyklus aufgelöst: eine frisch gestagte Datei
mit derselben Dateiidentität bedeutet Verschieben, sonst Löschung.

**Voll-Walk** (ADR-0037): Komplette Traversierung eines Sync Roots gegen den lokalen
DB-Index, um Offline-Änderungen nachzuholen. Läuft bei Start, Reconnect und als
Sicherheitsnetz-Lauf, gechunkt, mit Checkpoint und fortsetzbar. Nicht: Reconcile, Full Scan.

**Sicherheitsnetz-Lauf** (ADR-0037): Periodischer Voll-Walk (vorläufig alle 20 Minuten), der
verlorene Watcher-Ereignisse abfängt, etwa bei Pufferüberlauf. Nicht die primäre
Änderungserkennung.

**Bulk-Delete-Guard** (ADR-0064): Pausiert den Sync Root und fragt nach, wenn eine
Löschwelle verdächtig groß ist (über 30 % der verfolgten Zeilen oder absolute Schwelle
`BULK_DELETE_ABSOLUTE_THRESHOLD`). Ordner zählen über ihre Nachfahren im lokalen Index.

## Web

**Sync-Ordner** (`SyncRoot`): Vom Server gelieferter Ordner der obersten Ebene, in der UI
als Eintrag unter `/files`. In deutschen Texten gilt "Sync-Ordner", nicht "Sync-Root".

**Datei-Eintrag** (`DirEntry`): Einheitliche Darstellung von Ordnern und Dateien in einem
Listing (`kind: 'dir' | 'file'`). Das optionale `mount`-Feld markiert die Herkunft aus einem
Shared Mount; die Weiche zwischen Sync-Root- und Mount-Endpoints hängt an `entry.mount`.

**Detail-Auswahl** (`DetailSelection`): Der im Detail-Panel gezeigte Eintrag; sie überlebt
Ansichtswechsel und Suchaktionen.

**Listing-Sektion** (`Section`): Gruppe innerhalb der Listing-Komponente (ADR-0012), zum
Beispiel "Eigene" und "Mit mir geteilt". Multiselect, Lasso und Sortierung respektieren
Sektionsgrenzen.

**Ansichten** (`FilesViewMode`): `table` (Standard) oder `grid`, global in `localStorage`
unter `privasync.filesViewMode` gespeichert.

**Sortierung** (`FilesViewSort`): Schlüssel `name`, `size`, `mtime`, je aufsteigend oder
absteigend, über `useTableSort` pro Tabelle persistiert. Tabellenkopf und Sort-Picker der
Toolbar teilen sich denselben Zustand.

**Vorschau** (`previewKindFor`): Einordnung einer Datei nach Endung (Bild, Video, Audio, PDF,
Archiv, Modell, Comic, EPUB, Schrift, Office). Steuert, was ein Doppelklick öffnet. Office
läuft über den externen Document Server, Audio und Video über einen Media-Stream-Grant.

**Thumbnail** (`RowThumb`): Lazy geladene Vorschau, Fallback auf das Dateityp-Icon. Größe
`sm` für Tabellenzeilen, `lg` für Raster-Kacheln.

**Datei-Tree** (`FileTree`): Linke Seitenleiste mit Sync-Ordnern und lazy geöffneten
Unterordnern; pro Nutzer abschaltbar.

## Gemeinsame UI-Bausteine (`@privasync/ui`)

**Primitive**: Basiskomponente ohne App-Logik, i18n oder Domänenannahmen (Button, Input,
Select, Dialog, ...).

**Token**: Design-Variable aus `theme.css` (Farben, Abstände, Radien). Apps definieren keine
parallelen Werte.

**Headless**: Komponente liefert Verhalten und Barrierefreiheit, das Aussehen kommt über
Tokens (Radix-Konvention).
