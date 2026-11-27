# ADR-0070: Der Start wartet weder auf den Platzhalter-Pass noch auf einen neu begonnenen Adopt-Pass

**Status:** Accepted
**Datum:** 2026-10-08

## Kontext

[ADR-0056](0056-walk-entkoppelt-uploads-laufen-mit.md) hat den Voll-Walk aus
`sync_cycle` herausgelöst. Zwei weitere Schritte vor `process_uploads` laufen
aber ebenfalls über den ganzen Root und hielten beim App-Start die Transfers
des Roots zurück:

- **Platzhalter-Pass** (`ensure_placeholders`): einmal pro Root und Sitzung,
  über jeden Eintrag. Er war gechunkt, damit die Engine-Loop und die
  CF-Callbacks Luft bekommen, wurde aber abgewartet. Gemessen am 2026-10-08:
  12,7k Einträge in 12,3 s, alle bereits Platzhalter, es gab also nichts zu
  tun. Linear hochgerechnet sind das bei 1 Mio. Dateien rund 17 Minuten
  Stillstand des Roots.
- **Adopt-Pass** (`adopt_on_first_sync`): läuft, solange kein
  `changes_cursor_<root>` existiert. Den schreibt erst `process_downloads`, und
  das kommt beim Erstsync erst nach einem stundenlangen Upload-Durchlauf. Wird
  die App vorher beendet, blättert der nächste Start den ganzen Server-Feed neu
  durch und hasht jede lokale Datei vollständig, auch die schon adoptierten.
  Das Hashen lief dazu noch synchron auf einem Executor-Thread.

## Entscheidung

**1. Der Platzhalter-Pass wird abgesetzt, nicht abgewartet.**
`spawn_ensure_placeholders` beansprucht den Root synchron und startet den Pass
dann als eigenen Task. Der Zyklus geht direkt weiter zu Uploads und Downloads.
Der Zustand pro Root (beansprucht, läuft, wie oft abgewiesen) liegt in
`PlaceholderPasses` hinter einem gemeinsamen Lock. Für einen abgewiesenen Pass
gelten weiter dieselben Regeln: erneut versuchen, höchstens
`PLACEHOLDER_MAX_ATTEMPTS`-mal.

**2. Ein laufender Pass zählt wie ein laufender Walk gegen „settled".** Solange
er läuft, setzt `settle_directory_status` den Root nicht auf in-sync: Der Pass
konvertiert gerade noch Verzeichnisse, deren Status das betrifft. Am Ende stößt
er `sync_now` an, damit ein Zyklus den Root abschließt.

**3. Die Konvertierung fragt zuerst mit einem Lese-Handle.**
`convert_to_placeholder` prüft per `FILE_READ_ATTRIBUTES`-Handle und
`CfGetPlaceholderInfo`, ob der Eintrag schon ein Platzhalter ist. Nur wenn
nicht, öffnet es mit Schreibrecht und konvertiert. Gemessen ist das isoliert
etwa 1,6-mal schneller für den Normalfall „schon konvertiert". Das Ergebnis ist
dasselbe, denn die Konvertierung eines bestehenden Platzhalters war ohnehin ein
Leerlauf.

**4. Der Adopt-Pass ist fortsetzbar.** Er führt einen eigenen Cursor
`adopt_cursor_<root>` und speichert ihn nach jeder Seite. `adopt_server_files`
überspringt Einträge ungehasht, deren Zeile schon `synced` mit genau dieser
`server_version` ist. Das Hashen läuft in `spawn_blocking`. `remove_sync_root`
räumt den Cursor mit ab, damit ein wieder hinzugefügter Root nicht mitten im
Feed anfängt.

## Konsequenzen

- Uploads und Downloads eines Roots starten nach dem App-Start ohne Wartezeit.
  Der Explorer-Status des Roots kommt erst nach dem Pass zurück, also so spät
  wie bisher, aber nicht später.
- Pass und Transfers laufen jetzt gleichzeitig. Eine Datei, die der Pass als
  noch nicht konvertiert sieht, kann zeitgleich von einem Upload konvertiert
  werden. Die zweite Konvertierung endet dann als `AlreadyPlaceholder`, und der
  Upload setzt den in-sync-Status danach selbst. Diesen Wettlauf gab es mit
  Explorer-Hydrierungen schon vorher.
- Ein Neustart nach abgebrochenem Erstsync kostet nur noch die Seiten, die
  der Adopt-Pass noch nicht gelesen hat, plus je eine DB-Abfrage pro Eintrag.
  Einmal adoptierte Dateien werden nicht mehr erneut gelesen.
- Verworfen: Subtree-Abfragen auf PK-Bereiche umzustellen, im Verdacht, den
  Pass quadratisch zu machen. Mit `PRAGMA case_sensitive_like = ON` nutzt
  SQLite den `LIKE`-Präfix bereits als Index-Range (gemessen: 0,03 ms pro
  Verzeichnis).
