# ADR-0052: Ordner-Status wird über eine Invalidierungs-Kette wiederhergestellt

**Status:** Accepted
**Datum:** 2026-08-27

## Kontext

Der Cloud-Filter unter Windows aggregiert den Synchronisierungsstatus eines
Ordners nicht selbst. Er löscht stattdessen das `IN_SYNC`-Bit eines
Verzeichnis-Placeholders, sobald sich irgendetwas darunter ändert — und setzt es
nie wieder. Wer die Statusspalte im Explorer richtig haben will, muss das Bit
nach getaner Arbeit selbst neu setzen, und zwar für jedes betroffene
Verzeichnis.

Bis hierher tat der Client das nur für den Sync-Root selbst. Für alles
darunter blieb die Spalte nach dem ersten Download dauerhaft auf
„Synchronisierung ausstehend", obwohl jede Datei darin synchron war. Zwei
Lücken wirkten zusammen:

- Die konvertierende Passe (`ensure_placeholders`) läuft einmal pro Session.
  Ordner, die ein Download danach anlegt, entstehen als gewöhnliche
  NTFS-Verzeichnisse und werden nie Placeholder — für den Filter sind sie
  statuslos.
- Ordner, die beim Start konvertiert wurden, verlieren ihr Bit beim ersten
  Download darunter. Danach setzte es niemand mehr.

Die naheliegende Reparatur — am Ende jedes Zyklus über den Root laufen und alle
Verzeichnisse neu markieren — kollidiert mit ADR-0037. Genau dieser Voll-Walk
pro Zyklus wurde dort abgeschafft, weil er bei Millionen Dateien den Zyklus
dominiert. Ein Walk, der „nur" Verzeichnisse anfasst, muss trotzdem jedes
Verzeichnis auslesen und kostet damit dieselbe Größenordnung.

Aus der lokalen DB lässt sich die Verzeichnismenge nicht ableiten: seit
ADR-0041 ist zwar ein Verzeichnis eine Zeile, aber gestagt wird nur das *leere*
Verzeichnis. Ordner mit Inhalt haben keine eigene Zeile — sie sind über ihre
Kinder impliziert.

## Entscheidung

**Wer etwas anfasst, meldet die Kette.** Jede Operation, die eine Datei oder ein
Verzeichnis auf der Platte verändert, trägt dessen Vorfahren-Kette bis unter den
Sync-Root in einen Per-Zyklus-Sammler `SyncEngine::dirty_dirs` ein
([`engine_placeholders.rs`](../../apps/desktop/src-tauri/src/sync/engine_placeholders.rs)).
Das ist exakt die Menge, deren Bits der Filter gelöscht hat — die Ursache der
Löschung ist ja die eigene Änderung. Ein Verzeichnis-Eintrag trägt sich zusätzlich
selbst ein.

Die Kette bricht ab, sobald ein Pfad schon im Set steht: seine Vorfahren kamen
beim selben Einfügen mit. Tausend Dateien in einem Ordner kosten damit einen
Eintrag, nicht tausend.

**Wiederhergestellt wird am Settled-Ende des Zyklus.** `settle_directory_status`
leert das Set des Roots und arbeitet es *tiefste zuerst* ab. Die Reihenfolge ist
tragend, nicht kosmetisch: das Setzen eines Kind-Bits löscht das des Elterns,
eine Eltern-zuerst-Reihenfolge würde sich selbst rückgängig machen. Zum Schluss
folgt der Sync-Root, wie bisher.

**Konvertieren statt nur markieren.** Jedes Verzeichnis der Menge geht durch
`convert_to_placeholder(..., in_sync = true)`. Das erledigt beide Fälle in einem
Aufruf: ein vom Download angelegter Ordner wird dabei erst zum Placeholder, und
`MARK_IN_SYNC` ist Teil der Konvertierung. Nur wenn der Ordner bereits
Placeholder war — die Konvertierung also ein No-op ist — folgt ein explizites
`mark_in_sync`.

**Der Sammler wird nur bei Bedarf gefüllt.** Auf Plattformen ohne Cloud-Files-API
schreibt `record_dirty_dirs` nichts; die Menge wäre dort eine Map, die niemand
liest.

**Die Session-Passe repariert Altlasten.** `ensure_placeholders` setzt beim
Bereits-Placeholder-Fall zusätzlich `mark_in_sync`. Damit kommt ein Ordner, dessen
letzter Zyklus nie settled war (Absturz, Beenden mitten im Transfer), beim
nächsten Start wieder in Ordnung, statt dauerhaft falsch zu bleiben.

Verworfen: der Verzeichnis-Walk pro Zyklus. Er ist die einzige Variante, die auch
ohne Buchführung korrekt ist, kostet aber genau das, was ADR-0037 abgeschafft
hat.

Verworfen: den bestehenden `transfers`-Sammler mitzubenutzen. Er trägt den
Root-*Namen* statt der Root-*Id* und wird von der Benachrichtigungs-Zusammen-
fassung zu einem anderen Zeitpunkt geleert — zwei Verbraucher mit
unterschiedlicher Lebensdauer an einem Puffer.

## Konsequenzen

**Positiv**

- Der Aufwand ist proportional zu dem, was sich bewegt hat, nicht zur Größe des
  Roots. Ein Zyklus ohne Änderungen markiert nur den Root.
- Vom Download angelegte Ordner bekommen ihren Status ohne einen zweiten
  Mechanismus — dieselbe Passe erledigt Konvertierung und Markierung.
- Die reine Kettenberechnung (`dirty_dir_chain`) ist ohne Windows-API testbar.

**Negativ**

- Die Korrektheit hängt an der Vollständigkeit der Meldestellen. Ein künftiger
  Pfad, der die Platte anfasst, ohne die Kette zu melden, hinterlässt wieder
  falsche Ordner. Die Meldung sitzt deshalb an den Engstellen
  (`apply_one_change`, der Upload-Task, die Hydrations-Passe), nicht an jedem
  einzelnen Schreibzugriff.
- Die Hintergrund-Hydration im Eager-Modus läuft fire-and-forget und landet
  möglicherweise nach dem Settle ihres eigenen Zyklus. Ihre Ordner sind erst
  einen Zyklus später richtig — bis zu `POLL_INTERVAL`. Auf gespawnte
  Hydrationen zu warten hieße, den Zyklus an den Transfer zu binden.
- Bleibt ein Root dauerhaft pending, wächst sein Set bis zum nächsten Settle. Die
  Schranke ist die Zahl verschiedener angefasster Verzeichnisse, nicht die der
  Operationen.
- Wird ein Sync-Root entfernt, bleibt sein Eintrag in der Map bis zum
  Prozessende liegen — wie bei `placeholder_init_done`.

## Referenzen

- ADR-0037 — kein Voll-Walk pro Zyklus; die Schranke, an der sich diese
  Entscheidung ausrichtet
- ADR-0041 — ein Verzeichnis ist eine Zeile; erklärt, warum die DB die
  Verzeichnismenge nicht liefert
