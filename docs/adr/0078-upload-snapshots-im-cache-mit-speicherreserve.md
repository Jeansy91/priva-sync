# ADR-0078: Upload-Snapshots liegen im verwalteten Cache mit Speicherreserve

**Status:** Superseded by ADR-0079
**Datum:** 2026-11-06

## Kontext

ADR-0077 erstellt vor jedem Upload eine konsistente Dateikopie. Eine Kopie neben
der Quelldatei kann nach einem harten Prozessabbruch liegen bleiben. Eine
pauschale Bereinigung von `.tmp`-Dateien ist unsicher: andere Programme können
dieselbe Endung verwenden, und Download-Staging folgt einem eigenen Lebenszyklus.
Zusätzlich kann eine oder können mehrere Kopien vorübergehend den verfügbaren
Speicher aufbrauchen.

## Entscheidung

- Upload-Snapshots liegen in einem eigenen `upload-snapshots`-Unterverzeichnis
  von Tauri `app_cache_dir()`, außerhalb der Sync Roots.
- Snapshots heißen `upload-snapshot-<UUID>.tmp`. Beim Start entfernt PrivaSync
  ausschließlich reguläre Dateien mit genau diesem Namensschema aus diesem
  Verzeichnis. Andere Dateien und Verzeichnisse bleiben unberührt.
- Vor dem Kopieren prüft PrivaSync den verfügbaren Speicher am Snapshot-Ziel.
  Eine gemeinsame Reservierung berücksichtigt parallele Kopien und hält
  mindestens 1 GiB frei. Reservierter Speicher wird beim Schreiben auf die
  tatsächlich belegte Dateigröße angerechnet.
- Bei unzureichendem Speicher vor dem Kopieren wird keine Kopie begonnen. Wird
  der Speicher während des Kopierens knapp, wird die Teilkopie entfernt. Der
  Upload bleibt retry-fähig und der Speicherfehler wird in der
  Sync-Problembehandlung angezeigt.
- Die Snapshot-Datei wird nach dem Upload oder bei einem Kopierfehler entfernt.
  Ein harter Prozessabbruch kann sie weiterhin hinterlassen; der nächste Start
  bereinigt sie anhand des eindeutigen Namensschemas.
- Schreibsperre der Quelle, Hash und Upload-Inhalt bleiben wie in ADR-0077
  beschrieben zusammengehörig. Download-Staging bleibt unverändert.

## Konsequenzen

- Der Cache kann auf einem anderen Volume als die Quelldatei liegen. Der
  Speicherplatz wird auf dem Volume des Cache-Verzeichnisses geprüft.
- Die 1-GiB-Reserve ist eine feste Grenze. Andere Prozesse können den freien
  Speicher nach der Prüfung verbrauchen; ein daraus entstehender Schreibfehler
  wird als fehlgeschlagener Upload behandelt und die Teilkopie bereinigt.
- Die Bereinigung greift nicht in Sync Roots oder allgemeine temporäre
  Verzeichnisse ein.
- Verwaiste Snapshots aus älteren Versionen können im Sync Root liegen bleiben:
  ihr damaliges Namensschema unterscheidet sie nicht sicher von `.tmp`-Dateien
  anderer Programme und wird deshalb nicht automatisch gelöscht.
