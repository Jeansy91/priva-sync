# ADR-0079: Chunked-Uploads setzen nur mit verifiziertem Snapshot fort

**Status:** Accepted
**Datum:** 2026-11-06

## Kontext

Ein unterbrochener Chunked-Upload kann über seine Serverseite fortgesetzt
werden. Dafür müssen die erneut gesendeten Chunks exakt zu den bereits
gespeicherten Chunks gehören. Ein Neustart darf weder einen inzwischen
geänderten Quelldateiinhalt fortsetzen noch einen nicht mehr benötigten
Snapshot dauerhaft im Cache behalten.

## Entscheidung

- Upload-Snapshots liegen im eigenen `upload-snapshots`-Unterverzeichnis von
  Tauri `app_cache_dir()`. Beim Erstellen gilt eine gemeinsame
  Speicherreservierung, die mindestens 1 GiB freien Speicher erhält.
- Das lokale Upload-Ledger speichert neben der Server-Session die UUID des
  Cache-Snapshots, dessen BLAKE3-Hash, Größe und Änderungszeit.
- Vor dem Fortsetzen öffnet PrivaSync die Quelle mit Schreibsperre und
  vergleicht ihren BLAKE3-Hash mit dem Ledger. Zusätzlich wird der Cache-
  Snapshot gehasht. Nur wenn Quelle und Snapshot beide den gespeicherten
  Hash und die Größe haben, darf PrivaSync die Session fortsetzen.
- Fehlt der Snapshot oder stimmt eine Prüfung nicht, wird der lokale
  Session-Eintrag verworfen und ein neuer Snapshot samt Upload erstellt.
- Beim Start bleiben ausschließlich wohlgeformte Cache-Snapshots erhalten,
  die von einem Ledger-Eintrag für einen noch ausstehenden Upload referenziert
  werden. Nicht mehr benötigte Einträge und nicht referenzierte Snapshots im
  PrivaSync-Verzeichnis werden entfernt; andere Dateien bleiben unberührt.
- Ein Snapshot bleibt nach einem unterbrochenen Upload nur erhalten, solange
  sein Ledger-Eintrag eine fortsetzbare Session referenziert. Nach Abschluss
  oder Verwerfen der Session wird er entfernt.

## Konsequenzen

- Nach einem Neustart kann ein unveränderter großer Upload fehlende Chunks
  fortsetzen, statt die Datei erneut vollständig zu übertragen.
- Bei verändertem Inhalt wird immer ein neuer Snapshot verwendet. Größe und
  Änderungszeit allein gelten nicht als Integritätsnachweis.
- Ein verwaister Server-Upload kann bis zum serverseitigen Ablauf bestehen
  bleiben, wenn sein lokaler Snapshot oder Ledger-Eintrag ungültig ist.
- Andere Dateien und Verzeichnisse im Cache werden von der Snapshot-
  Bereinigung nicht berührt.
