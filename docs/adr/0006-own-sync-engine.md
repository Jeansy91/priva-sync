# ADR-0006: Eigene Sync-Engine in Rust statt Rclone-Wrapper

**Status:** Accepted
**Datum:** 2026-04-30

## Kontext

Der Desktop-Client muss bidirektional syncen mit Konflikt-Erkennung,
Delta-Upload und nativer OS-Integration (File-Watcher, Cloud Files API,
Tray-Icon).

Rclone als Subprozess wäre günstig, ist aber generischer FS-Sync ohne
Konflikt-Erkennung für viele kleine Saves — `rclone bisync` ist experi-
mentell und nicht für diesen Lastfall gedacht. Der Client braucht volle
Kontrolle über Konflikt-Semantik, Versionierung und Quota-Feedback.

## Entscheidung

Eigene Sync-Engine in Rust, eng integriert in den Tauri-Client.

- File-Watcher via `notify` (inotify / FSEvents / `ReadDirectoryChangesW`),
  250 ms Debounce.
- Lokaler State in SQLite (`local_files` mit `sync_status`-Enum).
- Delta-Protokoll wie in [ADR-0002](0002-delta-sync-blake3.md).
- Konflikt-Modell wie in [ADR-0003](0003-conflict-loser-copy.md).
- Cloud Files API via WinRT, siehe [ADR-0005](0005-cloud-files-winrt.md).

Rclone bleibt als **externer WebDAV-Client** nutzbar (siehe Server-WebDAV-
Endpoint), aber nicht eingebaut.

## Konsequenzen

**Positiv:**
- Volle Kontrolle über Konflikt-Semantik, Versionierung, Quota-Feedback.
- Engine kann tief in OS-Features greifen (Cloud Files, Shell-Integration).
- Binary bleibt schlank (~10 MB), kein Rclone-Go-Subprozess.

**Negativ:**
- Viel Eigenbau — Resume, Retry, Bandbreiten-Limits, Pause-Logik müssen
  selbst geschrieben werden.
- Pro Plattform OS-spezifische Code-Pfade (CF-API Win, Spotlight macOS).
