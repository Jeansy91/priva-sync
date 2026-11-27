# Architekturüberblick

PrivaSync ist ein selbst gehosteter Datei-Sync mit zentralem Server, Desktop-Client,
Web-Client und eigener Sync-Engine. Korrektheit bei parallelen Schreibzugriffen ist eine
harte Anforderung, Geschwindigkeit kommt danach. Die vollständige Spezifikation steht in
[`../SPEC.md`](../SPEC.md); dieses Dokument ist die Kurzfassung.

## Komponenten

| Teil | Technik | Verzeichnis |
| --- | --- | --- |
| Server | Go (Standardbibliothek, chi), PostgreSQL | `apps/server` |
| Desktop-Client | Tauri v2 (Rust-Shell) + TypeScript, eigene Sync-Engine in Rust | `apps/desktop` |
| Web-Client | React 19 + Vite | `apps/web` |
| Geteilter API-Client | TypeScript, von Web und Desktop genutzt (`@privasync/api-client`) | `packages/api-client` |
| Geteilte UI-Bausteine | React-Komponenten, Design-Tokens (`@privasync/ui`) | `packages/ui` |
| Übersetzungen | `@privasync/i18n` | `packages/i18n` |

Das Repository ist ein Monorepo: pnpm-Workspace für JS/TS, ein Go-Modul für den Server,
ein Cargo-Workspace für den Desktop-Client.

## Datenfluss

- Desktop und Web sprechen mit dem Server über REST (`/api/v1/...`) und SSE
  (`/sse/events`) und nutzen dafür denselben API-Client.
- WebDAV (eigener Port, App-Passwörter, ADR-0040) ist ein zusätzlicher Zugang für Rclone,
  Explorer und Finder. Er läuft wie alles andere über den Sync Coordinator (ADR-0018).
- Der Server ist die einzige Wahrheit für Dateizustand, Quota, Papierkorb, Freigaben und
  Konflikte. Clients spiegeln diesen Zustand lokal.
- Der Server speichert Dateien im Klartext im Dateisystem; PostgreSQL hält Index und
  Metadaten. Sidecar-JSONs ermöglichen die Wiederherstellung (ADR-0001).

## Server-Kern

- **Sync Coordinator**: orchestriert alle mutierenden Operationen (Upload, Move, Delete,
  Restore, Papierkorb leeren) als Unit of Work (ADR-0022). Er prüft die Quota vor dem
  Staging und unterscheidet Versionskonflikte (`If-Match`, HTTP 409) von echten
  Konflikten.
- **Outbox**: Seiteneffekte (Storage-Rename, SSE, Mail) werden transaktional vorgemerkt
  und von einem In-Process-Worker abgearbeitet, mit Fallback-Poll für Crash-Recovery.
- **Domain Event Bus**: Publish/Subscribe für Domänenereignisse; kritische Subscriber
  laufen in der Transaktion, reaktive nach dem Commit.
- **Changes-Feed**: Clients holen Änderungen über einen monotonen Sequenz-Cursor, nicht
  über Zeitstempel (ADR-0036); Ordner-Moves reisen als ein Präfix-Ereignis (ADR-0065).
- **Eine Instanz**: Der Server ist bewusst nicht horizontal skalierbar (ADR-0039).

## Desktop-Sync-Engine

- Entscheidungslogik ist rein, die Ausführung davon getrennt (ADR-0024).
- Änderungserkennung läuft ereignisbasiert über den Dateisystem-Watcher; ein gechunkter,
  fortsetzbarer Voll-Walk fängt Offline-Änderungen und Ereignisverluste auf (ADR-0037,
  ADR-0056, ADR-0058).
- Verschieben wird über die Dateiidentität erkannt, nicht über den Namen (ADR-0063).
- Windows-Platzhalter nutzen die Cloud Files API (ADR-0005); On-Demand-Hydration läuft
  mit Hash-Provenienz und Versions-Pin (ADR-0048).
- Auth-Refresh und Server-Zugriff laufen zentral über `AuthedApi` (ADR-0025, ADR-0071).

## Web-Client

- Die URL ist die Navigationswahrheit: aktueller Ordner und Suchanfrage stehen in der
  Route, nicht in lokalem State.
- Eine vereinheitlichte Listing-Komponente rendert Tabelle und Raster inklusive
  geteilter Einhängepunkte (ADR-0012).
- Sichtbare Texte laufen über `@privasync/i18n`.

## Sicherheit und Betrieb

- Auth über kurzlebige Access-Tokens mit rotierendem Refresh-Token (ADR-0042, ADR-0046),
  optional TOTP und Passkeys (ADR-0026). SSE nutzt ein eigenes Cookie (ADR-0038).
- Security-Audit ist Release-Gate in der CI (ADR-0007, ADR-0030, ADR-0031).
- Client-Updates: Der Server bietet nur die Client-Version seiner eigenen Version an. Der
  Update-Feed (`/updates/{target}/{version}`) verweist direkt auf die GitHub-Release-Assets;
  Asset-Namen und Signaturen stehen in einem Manifest, das die CI vor dem Image-Build
  einbettet (ADR-0088). Das Repository muss dafür öffentlich sein.
- Der Server-Container läuft als Non-root-User (ADR-0034).

Die Begründung jeder einzelnen Entscheidung steht in der jeweiligen ADR, siehe
[`adr/README.md`](adr/README.md).
