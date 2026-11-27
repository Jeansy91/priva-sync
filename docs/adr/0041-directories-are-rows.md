# ADR-0041: Ein Verzeichnis ist eine Zeile, keine Ableitung

**Status:** Accepted
**Datum:** 2026-08-25

## Kontext

Die Verzeichnisstruktur wurde bisher aus den Dateipfaden abgeleitet. Eine
Ebene aufzulisten hieß: jede Zeile des Teilbaums auf ihr erstes Segment
reduzieren, gruppieren, die Seite nehmen. Eine eigene Zeile hatte ein
Verzeichnis nur, wenn es explizit als leerer Ordner angelegt worden war
(Migration 016).

Damit richten sich die Kosten einer Auflistung nach der Größe des Teilbaums,
nicht nach der Größe der Seite. Gemessen an einem Root mit 1 Mio. Zeilen:

| Ebene | vorher | nachher | mit Bereichs-Index |
|---|---|---|---|
| `/dir0001/` | 1m55s | 430 ms | 79 ms |
| `/` (oberste Ebene) | — | 1,99 s | 1,89 s |

Die obere Ebene lässt sich so nicht verbessern: der Bereich unterhalb von `/`
umfasst berechtigterweise alle 1 Mio. Zeilen. Ein Index-Scan über den ganzen
Root bleibt ein Scan über den ganzen Root.

Die Ableitung hatte zwei weitere Folgen, die nie entschieden wurden, sondern
aus ihr herausfielen:

- Ein Ordner, dessen letzte Datei gelöscht wurde, verschwand. Er hatte nie
  eine eigene Existenz.
- Der Rückgabetyp `LevelEntry` trug ein `*model.File`, das für implizite
  Verzeichnisse `nil` war. Jeder Konsument — vier Handler und der
  WebDAV-Adapter — brauchte einen Zweig für den halben Eintrag.

Setzt auf [ADR-0008](0008-files-listing-and-search-scale.md) auf und ersetzt dessen
Ableitungsverfahren.

## Entscheidung

Jedes Verzeichnis ist eine Zeile in `files` mit `is_dir = TRUE`. Vier Teile:

**1. `parent_path` als abgeleitete Spalte.** `files.parent_path` enthält das
enthaltende Verzeichnis samt Trennzeichen, sodass `parent_path || <name>`
den Pfad rekonstruiert. Zeilen der obersten Ebene tragen `/`.

Gesetzt wird die Spalte von einem `BEFORE INSERT OR UPDATE`-Trigger, nie von
der Anwendung. Damit können `path` und `parent_path` nicht auseinanderlaufen
— auch nicht beim Massen-`UPDATE`, das einen ganzen Teilbaum umschreibt. Ein
Verlass auf elf Schreibpfade wäre elf Gelegenheiten, es zu vergessen.

**2. Auflisten ist ein Index-Bereich.** `ListDirectoryLevel` liest
`WHERE sync_root_id = $1 AND parent_path = $2 AND path > $cursor`, gedeckt
von `idx_files_level (sync_root_id, parent_path, path) WHERE is_deleted =
FALSE`. Die Kosten richten sich nach der Seite, unabhängig von der Größe des
Roots.

**3. Materialisierung an jedem Schreibpfad.** `ensureAncestors` legt die
Verzeichnisse oberhalb eines neuen Pfades an — idempotent, per
`ON CONFLICT DO NOTHING`. Aufgerufen von `Upsert`, `CreateDirectory`,
`MoveFile`, `MoveDirectory`, `CopyFile`, `CopyDirectory` und beiden
Restore-Pfaden.

Bei den erzeugenden Pfaden geschieht das **vor** der eigenen Zeile, damit das
Verzeichnis die kleinere `seq` trägt: ein Client, der den Changes-Feed
durchblättert, erfährt vom Ordner, bevor er die Datei darin sieht. Beim
Restore geht es nicht, weil dort erst die Zeile den Pfad verrät — dort ist
die Reihenfolge umgekehrt und folgenlos, da ein Client beim Schreiben einer
Datei ihr Verzeichnis ohnehin anlegt.

**4. Ein Repair-Sweep als Netz.** `directory_repair` (wöchentlich, in der
Job-Registry) zieht fehlende Verzeichnis-Zeilen nach. Er existiert, weil der
Fehler, gegen den er sichert, stumm ist: die Zeilen unterhalb eines
fehlenden Verzeichnisses synchronisieren weiter und sind nur über die
Navigation nicht mehr erreichbar.

## Konsequenzen

**Ein Ordner überlebt seine letzte Datei.** Näher am Dateisystem als vorher,
aber ein sichtbarer Wechsel für Nutzer und Clients.

**Einmalig ein Massen-Delta.** Die materialisierten Verzeichnisse bekommen
eine `seq` und erreichen jeden Client einmal über den Changes-Feed —
paginiert nach [ADR-0036](0036-changes-feed-sequence-cursor.md). Der Desktop
verarbeitet sie mit `create_dir_all` + `upsert_dir`, beides idempotent.

**„Eine Zeile am Pfad" heißt nicht mehr „eine Datei".** Vier Handler haben
aus der Existenz einer Zeile auf eine Datei geschlossen — das stimmte nur,
solange Verzeichnisse keine hatten. Ohne Korrektur wäre jede
Verzeichnis-Freigabe zu einer Datei-Freigabe geworden. Ersetzt durch
`isLiveFile`, das zusätzlich `is_dir` prüft. Derselbe Fehler traf schon
vorher explizit angelegte leere Ordner.

**`LevelEntry` entfällt.** `ListDirectoryLevel` gibt `[]model.File` zurück;
der `nil`-Zweig bei den Konsumenten ist weg. Die JSON-Antwort behält ihre
Form — Verzeichnisse tragen jetzt zusätzlich `id`, `mtime` und `updated_at`.

**Eine Datei am Verzeichnispfad wird gemeldet, nicht überschrieben.** Ein
Zustand aus der Zeit vor dieser ADR und in beide Richtungen widersprüchlich;
eine Datei zugunsten einer Invariante zu zerstören wäre die schlechtere
Antwort.

**Mehr Zeilen.** Eine je Verzeichnis. Gegenüber der Zahl der Dateien
vernachlässigbar.

## Verworfene Alternative

**Ancestors im Trigger anlegen statt in Go.** Ein `BEFORE INSERT`-Trigger,
der sich rekursiv nach oben arbeitet, wäre nicht zu umgehen. Er vergibt
seinen Ancestors aber größere `seq`-Werte als dem Kind, das ihn ausgelöst
hat, und rekursives Trigger-Feuern auf derselben Tabelle ist schwerer zu
prüfen als ein Aufruf, den ein Test rot werden lässt. Die zehn
Invarianten-Tests fallen ohne `ensureAncestors` alle um; das ist der Ersatz
für die Unumgehbarkeit.
