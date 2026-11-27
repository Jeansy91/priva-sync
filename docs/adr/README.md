# Architecture Decision Records

System-weite Entscheidungen für priva-sync. Eine Datei pro Entscheidung,
unveränderlich nach Akzeptanz — Änderungen entstehen als neue ADR, die die
vorherige `Superseded` markiert.

Context-spezifische Entscheidungen leben unter `apps/<context>/docs/adr/`
bzw. `packages/<context>/docs/adr/`. Zurzeit gibt es eine solche ADR:
[Server ADR-0001 — Repository-Split nach Aggregaten](../../apps/server/docs/adr/0001-repository-aggregate-split.md).

Zurück zur [Doku-Übersicht](../README.md).

## Format

```
# ADR-NNNN: Kurz-Titel

**Status:** Accepted | Superseded by ADR-XXXX | Deprecated
**Datum:** YYYY-MM-DD

## Kontext
Was war das Problem, welche Constraints galten?

## Entscheidung
Was wurde beschlossen?

## Konsequenzen
Positive + negative Folgen, Aufwand, was später anders gemacht werden müsste.
```

## Index

- [ADR-0001 — Dateisystem-Resilienz via JSON-Sidecars](0001-fs-resilience-sidecars.md)
- [ADR-0002 — Delta-Sync mit BLAKE3 + rsync-Block-Hashes](0002-delta-sync-blake3.md)
- [ADR-0003 — Konflikte als Loser-Kopie + DB-Row](0003-conflict-loser-copy.md)
- [ADR-0004 — Trash zählt zur Quota (kein separater Topf)](0004-trash-counts-toward-quota.md)
- [ADR-0005 — Cloud Files via WinRT `StorageProviderSyncRootManager`](0005-cloud-files-winrt.md)
- [ADR-0006 — Eigene Sync-Engine in Rust statt Rclone-Wrapper](0006-own-sync-engine.md)
- [ADR-0007 — Security-Audit als Release-Gate](0007-security-audit-release-gate.md)
- [ADR-0008 — Files-Listing und Suche auf Million-File-Roots](0008-files-listing-and-search-scale.md)
- [ADR-0009 — Eigentümer und letzter Bearbeiter in der Detailansicht](0009-detail-panel-owner-and-last-modifier.md)
- [ADR-0010 — Shared Mounts: Empfänger-Sicht auf eingehende user-Shares](0010-shared-mounts-recipient-view.md)
- [ADR-0011 — Shared Mounts: Desktop-Sync](0011-shared-mounts-desktop-sync.md)
- [ADR-0012 — Vereinheitlichte Listing-View und Mount-Decoration](0012-unified-listing-view-and-mount-decoration.md)
- [ADR-0013 — Letzter Bearbeiter pro Datei (zieht ADR-0009 Stufe-2 vor)](0013-last-modifier-tracking.md)
- [ADR-0014 — Share-Permission-Wechsel ohne Widerruf (superseded by ADR-0015)](0014-share-permission-update.md)
- [ADR-0015 — Eine User-Freigabe pro Pfad (Grants) + Public-Dual-Link](0015-share-grants-and-public-dual-link.md)
- [ADR-0016 — Konflikt-Auflösung mit Strategien (keep_current / keep_conflict / keep_both)](0016-conflict-resolution-strategies.md)
- [ADR-0017 — File-Mutation-Handler-Dedup via MutationContext, Coordinator bleibt tief](0017-file-mutation-handler-dedup.md)
- [ADR-0018 — WebDAV als FileSystem-Adapter über dem Sync Coordinator](0018-webdav-over-coordinator.md)
- [ADR-0019 — WebDAV PROPPATCH ohne Dead-Property-Store (vorerst)](0019-webdav-proppatch-no-dead-props.md)
- [ADR-0020 — Globale Sync-Ausschlussmuster (.gitignore-Semantik)](0020-sync-exclusion-patterns.md)
- [ADR-0021 — Explorer-Kontextmenü über Shell-Ext + Named-Pipe + Daemon-Service](0021-explorer-context-menu.md)
- [ADR-0022 — Coordinator-Mutation als Unit-of-Work (TX-Runner, Outbox-Batch, ein Conflict-Materialisierer)](0022-coordinator-unit-of-work.md)
- [ADR-0023 — SSE-Liefersemantik explizit: Durable via Outbox, Ephemeral via Hub](0023-sse-delivery-semantics.md)
- [ADR-0024 — Desktop-Sync-Engine: Entscheidungslogik pur, Ausführung getrennt](0024-desktop-engine-decision-execution-split.md)
- [ADR-0025 — Desktop Auth-Refresh als API-Client-Seam](0025-desktop-auth-refresh-seam.md)
- [ADR-0026 — Passkey/WebAuthn-Login als eigenständige Anmeldemethode](0026-passkey-webauthn-login.md)
- [ADR-0027 — Euro-Office als externer, konfigurierbarer Document-Server statt lokalem OnlyOffice-Container](0027-euro-office-external-document-server.md)
- [ADR-0028 — Coverage-Basis fuer das AC-Ziel des Servers auf den testbaren Kern](0028-coverage-basis-scope.md)
- [ADR-0029 — Handler-Repository-Entkopplung ueber schmale Rollen-Interfaces](0029-handler-repository-seam.md)
- [ADR-0030 — Security-CI-Gates kalibrieren statt Rauschen unterdruecken](0030-security-gate-calibration.md)
- [ADR-0031 — Security-Findings-Review: Inline-Suppression, echte Fixes, ein bewusstes Deferral](0031-security-findings-review.md)
- [ADR-0032 — pnpm `trustPolicyExclude` fuer zwei bekannte False-Positives](0032-pnpm-trustpolicy-exclude.md)
- [ADR-0033 — Euro-Office Bearbeiten + Speichern über opaken Callback-Token und bestehende Konflikt-Pipeline](0033-euro-office-edit-save.md)
- [ADR-0034 — Non-root Container-USER mit fester UID](0034-nonroot-container-user.md)
- [ADR-0035 — Admin-Release-Verwaltung: server-seitiger Manifest-Bau, inkrementelles Upload-Modell](0035-admin-release-management.md) *(superseded von ADR-0088)*
- [ADR-0036 — Changes-Feed mit monotoner Sequenz statt Timestamp-Cursor](0036-changes-feed-sequence-cursor.md)
- [ADR-0037 — Lokaler Reconcile: selten, gechunkt, hierarchischer Diff statt Voll-Walk pro Zyklus](0037-local-reconcile-scale-redesign.md)
- [ADR-0038 — SSE-Stream authentifiziert über ein eigenes Cookie, nicht über die URL](0038-sse-cookie-auth.md)
- [ADR-0039 — Genau eine Serverinstanz](0039-single-server-instance.md)
- [ADR-0040 — WebDAV authentifiziert über App-Passwörter, nicht über das Kontopasswort](0040-webdav-app-passwords.md)
- [ADR-0041 — Ein Verzeichnis ist eine Zeile, keine Ableitung](0041-directories-are-rows.md)
- [ADR-0042 — Eine Session endet nur, wenn der Server den Refresh-Token ablehnt](0042-session-survives-refresh-races-and-blips.md) *(superseded von ADR-0046)*
- [ADR-0043 — Complete nimmt an, ein eigener Worker assembliert](0043-chunked-upload-assembly-worker.md)
- [ADR-0044 — Ein CI-Workflow, Builds hinter denselben Gates](0044-single-ci-workflow.md) *(Gate-auf-Tag-Regel abgelöst von ADR-0053)*
- [ADR-0045 — Chunks schreiben an ihren Offset, ein Lesedurchgang publiziert](0045-chunks-write-at-offset.md) *(Adressierung abgelöst von ADR-0066)*
- [ADR-0046 — Eine Session endet nur bei Ablehnung — in jedem Client, und die Rotation bekommt ein Kulanzfenster](0046-refresh-rotation-grace-window.md)
- [ADR-0047 — Downloads streamen über eine gestagte Temp-Datei](0047-downloads-stream-through-a-staged-temp-file.md)
- [ADR-0048 — Hash-Provenienz und Versions-Pin der Cloud-Filter-Hydration](0048-placeholder-hash-provenance-and-version-pin.md)
- [ADR-0049 — Office-Formatumfang und Round-Trip-Editierbarkeit](0049-office-format-range-and-round-trip-editability.md)
- [ADR-0050 — Der Desktop-Client schreibt ein rotierendes Dateilog](0050-desktop-client-schreibt-ein-rotierendes-dateilog.md)
- [ADR-0051 — Löschen mit If-Match](0051-delete-mit-if-match.md)
- [ADR-0052 — Ordner-Status wird über eine Invalidierungs-Kette wiederhergestellt](0052-ordner-status-per-invalidierungs-kette.md)
- [ADR-0053 — CI setzt den Tag, der Tag baut nur noch](0053-ci-taggt-selbst-nach-gruenen-gates.md) *(superseded von ADR-0054)*
- [ADR-0054 — Ein Lauf pro Release: Gates, Tag und Publish in derselben Ausführung](0054-ein-lauf-pro-release.md) *(Versions-Weitergabe abgelöst von ADR-0055)*
- [ADR-0055 — Job-Outputs nur als Ja/Nein, Werte aus der Quelle](0055-job-output-nur-als-ja-nein.md)
- [ADR-0056 — Der Voll-Walk läuft entkoppelt, Uploads laufen mit](0056-walk-entkoppelt-uploads-laufen-mit.md)
- [ADR-0057 — Ein Sync-Root auf einem Laufwerks-Root synchronisiert ohne Platzhalter](0057-laufwerks-roots-synchronisieren-ohne-platzhalter.md)
- [ADR-0058 — Der Walk-Checkpoint steht auf jeder Ebene, nicht nur auf der obersten](0058-walk-checkpoint-auf-jeder-ebene.md)
- [ADR-0059 — Live-Änderungen überholen die Walk-Warteschlange](0059-live-aenderungen-ueberholen-die-walk-warteschlange.md)
- [ADR-0060 — Clients aktualisieren sich beim Start selbst, vor allem anderen](0060-clients-aktualisieren-sich-beim-start-selbst.md)
- [ADR-0061 — Geräte registrieren sich bei jedem Refresh, Löschen ist reine Buchführung](0061-geraete-registrieren-sich-bei-jedem-refresh.md)
- [ADR-0062 — Ein offener Änderungs-Auftrag benennt den Pfad, nicht den Zustand](0062-offener-aenderungs-auftrag-benennt-den-pfad.md)
- [ADR-0063 — Verschieben wird an der Dateiidentität erkannt, nicht am Namen](0063-verschieben-an-der-dateiidentitaet-erkennen.md)
- [ADR-0064 — Bulk-Delete-Guard zählt Ordner über Nachfahren](0064-bulk-delete-guard-zaehlt-ordner-ueber-nachfahren.md)
- [ADR-0065 — Teilbaum-Move reist als ein Präfix-Ereignis](0065-teilbaum-move-reist-als-ein-praefix-ereignis.md)
- [ADR-0066 — Chunk-Grösse passt sich pro Chunk an, Adressierung wird Byte-Range](0066-chunk-groesse-passt-sich-pro-chunk-an.md)
- [ADR-0067 — Web-Audio/Video streamt über einen Media-Grant statt Voll-Download](0067-web-media-streaming-ueber-grant-token.md)
- [ADR-0068 — Client-Import aus konfigurierbarer Release-Quelle](0068-client-import-aus-konfigurierbarer-release-quelle.md) *(superseded von ADR-0088)*
- [ADR-0069 — Desktop-Commands melden Fehler als `{error, code}`](0069-desktop-command-fehler-als-code.md)
- [ADR-0070 — Der Start wartet weder auf den Platzhalter-Pass noch auf einen neu begonnenen Adopt-Pass](0070-start-wartet-weder-auf-platzhalter-pass-noch-auf-adopt-neustart.md)
- [ADR-0071 — Die Sync-Engine erreicht den Server nur über `AuthedApi`](0071-sync-engine-erreicht-den-server-nur-ueber-authedapi.md)
- [ADR-0072 — Lese- und Schreib-Timeout sind Idle-Timeouts](0072-lese-und-schreib-timeout-sind-idle-timeouts.md)
- [ADR-0073 — Browser-Downloads laufen über den Download-Manager des Browsers](0073-browser-downloads-laufen-ueber-den-download-manager.md)
- [ADR-0074 — Mehrere Sync-Roots erscheinen unter einem PrivaSync-Namespace](0074-explorer-namespace-for-multiple-sync-roots.md)
- [ADR-0075 — Explorer-Namespace zeigt Sync-Roots mit ihren Ordner-Icons](0075-explorer-namespace-links-preserve-root-icons.md)
- [ADR-0076 — Explorer-Namespace bindet Sync-Roots als aufklappbare Ordner](0076-explorer-namespace-expandable-sync-roots.md)
- [ADR-0077 — Uploads lesen aus einem Snapshot, der unter Schreibsperre entsteht](0077-uploads-lesen-aus-einem-gesperrten-snapshot.md)
- [ADR-0078 — Upload-Snapshots liegen im verwalteten Cache mit Speicherreserve](0078-upload-snapshots-im-cache-mit-speicherreserve.md) *(superseded by ADR-0079)*
- [ADR-0079 — Chunked-Uploads setzen nur mit verifiziertem Snapshot fort](0079-chunked-upload-resume-mit-verifiziertem-snapshot.md)
- [ADR-0080 — Windows-11-Explorer-Menü über Sparse Package](0080-windows-11-explorer-menue-ueber-sparse-package.md) *(superseded von ADR-0081)*
- [ADR-0081 — Maschinenweiter Trust für das Windows-11-Sparse-Package](0081-windows-11-sparse-package-machine-trust.md)
- [ADR-0082 — Explorer-Menü-Icons aus dem Web-Iconset und flaches Untermenü](0082-explorer-menue-icons-und-flaches-untermenue.md)
- [ADR-0083 — Legacy-Dateien als Jet-Datenbanken sperren, Konflikt-Mails als Tagesdigest](0083-legacy-lockdatei-und-konflikt-tagesdigest.md)
- [ADR-0084 — Konfliktvergleich zeigt exakte Größe, Datum und Bildvorschau](0084-konfliktvergleich-mit-datum-groesse-und-bildvorschau.md)
- [ADR-0085 — Identische Uploads sind kein Konflikt](0085-identische-uploads-sind-kein-konflikt.md)
- [ADR-0086 — GitHub-Releases als Release-Quelle](0086-github-releases-als-release-quelle.md) *(superseded von ADR-0088)*
- [ADR-0087 — App-Identifier, Herausgeber und Update-Endpunkt aus der Server-URL](0087-app-identifier-und-update-endpunkt.md)
- [ADR-0088 — Client-Updates direkt von GitHub, immer passend zur Server-Version](0088-client-updates-direkt-von-github-passend-zur-server-version.md)
