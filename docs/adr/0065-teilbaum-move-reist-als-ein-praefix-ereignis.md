# ADR-0065: Teilbaum-Move reist als ein Präfix-Ereignis im Changes-Feed

**Status:** Accepted
**Datum:** 2026-09-04

## Kontext

`MoveDirectory` ([file_mutate.go:190](../../apps/server/internal/repository/file_mutate.go#L190))
rewritet in einem Statement jede Nachfahren-Zeile eines verschobenen Ordners
und setzt an jedem verlassenen Pfad einen Tombstone. Der `files_assign_seq`-
Trigger (Migration 032, ADR-0036) vergibt bei jedem UPDATE ein frisches `seq`
— ein Move über 400 000 Dateien erzeugt so ~800 000 Einträge im **Changes-
Feed**, obwohl der Client davon genau einen braucht: `detect_renames` paart
ohnehin id-basiert, und die Umbenennung des Elternverzeichnisses verschiebt
den ganzen Teilbaum in einer einzigen Dateisystem-Operation.

Jenseits von `MAX_DEFERRED_TOMBSTONES = 10_000`
([mod.rs:60](../../apps/desktop/src-tauri/src/sync/mod.rs#L60)) wird die
Tombstone-Paarung ausserdem blind — ein Move über mehr als 10 000 Dateien ist
damit nicht nur ein Verkehrsproblem, sondern ein Korrektheitsrisiko: ein
Tombstone, dessen Gegenstück erst Seiten später kommt, löscht lokal Inhalt,
der nur verschoben wurde. Als bekannte Grenze in [ADR-0063](0063-verschieben-an-der-dateiidentitaet-erkennen.md)
festgehalten, hier entschieden.

## Entscheidung

Ein Teilbaum-Move reist als **ein** Ereignis, nicht als eine Zeile pro
Nachfahre.

**Eigene Tabelle `move_events`** (`sync_root_id`, `old_prefix`, `new_prefix`,
`seq`) statt einer Sonderzeile in `files` — `files` bleibt reiner
Zeilenzustand, das Move-Ereignis ist ein eigenes, kurzlebiges Konzept mit
eigener Lebensdauer. `seq` kommt aus derselben `files_seq_seq`-Sequenz wie
File-Zeilen, damit beide Quellen eine gemeinsame Ordnung teilen.

**Kein `seq`-Bump auf den betroffenen `files`-Zeilen.** `MoveDirectory` läuft
neu in einer expliziten Transaktion (`ensureAncestors` + Bulk-UPDATE +
`move_events`-Insert atomar) und klammert das Bulk-UPDATE eng in
`SET LOCAL app.suppress_seq_bump = 'on' … RESET`. Der Trigger prüft
`current_setting('app.suppress_seq_bump', true)` und behält `NEW.seq :=
OLD.seq`, statt `nextval()` zu ziehen. Verworfen: `ALTER TABLE … DISABLE
TRIGGER` um das Statement — zieht `SHARE ROW EXCLUSIVE` auf `files` und
blockiert damit jeden gleichzeitigen Schreiber auf der ganzen Tabelle, nicht
nur auf dem betroffenen Sync Root, für die volle Dauer eines
Millionen-Zeilen-Updates.

**Keine Tombstones mehr an den verlassenen Pfaden.** Das `move_events`-
Ereignis ersetzt sie vollständig; das Deckel-Risiko bei
`MAX_DEFERRED_TOMBSTONES` entfällt, weil gar keine Move-Tombstones mehr
entstehen. `MoveFile` für eine einzelne Datei ist nicht betroffen — Tombstone
und Zeilen-Bump bleiben, `detect_renames` paart sie heute schon korrekt über
die File-Id.

**Feed liefert beide Quellen gemerged.** `GetChangedSince` und
`ListChangedFilesSinceUnderPrefix` fragen `files` und `move_events` mit
`seq > cursor` je auf `limit+1` begrenzt ab, mergen die Ergebnisse nach
`seq` aufsteigend, kappen auf `limit` und setzen `next_cursor` auf das
`seq` des zuletzt ausgelieferten Eintrags — das `+1`-Overfetch pro Quelle
macht "gibt es mehr" unabhängig vom Merge-Ergebnis feststellbar.

**Client deklariert die Fähigkeit, sonst lauter Fehler statt stiller
Divergenz.** Beide `/changes`-Endpunkte (eigener Sync Root und Shared Mount
— beide von derselben Rust-Engine konsumiert) verlangen ab sofort
`feed_version=2`; fehlt der Parameter, antwortet der Server mit `400
ErrFeedVersionRequired`, unabhängig davon, ob gerade ein Move ansteht.
Vertretbar, weil seit [ADR-0060](0060-clients-aktualisieren-sich-beim-start-selbst.md)
jeder Client bei jedem Start selbst aktualisiert — das Fenster, in dem ein
Client den Fehler sieht, sind Minuten. Verworfen: den Fehler nur auslösen,
wenn ein Move tatsächlich aussteht — ein Sonderfall mehr in der
meistgelesenen Query für einen in der Praxis nicht spürbaren Unterschied.

**Client wendet das Präfix-Ereignis über den bestehenden Rename-Pfad an.**
`db.rename_file` (Desktop-SQLite) macht die Präfix-Splice über den ganzen
Teilbaum bereits in einer Transaktion und wird heute schon von
`apply_renames` für id-erkannte Ordner-Umbenennungen benutzt
([reconcile.rs:333](../../apps/desktop/src-tauri/src/sync/reconcile.rs#L333)).
Ein `move_events`-Eintrag wird zu genau demselben `(old_prefix, new_prefix)`-
Paar geparst, shallowest-first in `plan.renames` gemerged und läuft
unverändert durch diese Schleife — kein neuer Anwendungspfad, keine
zusätzliche Watcher-Suppression pro Nachfahre nötig (Windows meldet für
einen Teilbaum-Move ohnehin nur ein Ereignis am Elternverzeichnis, ADR-0063).

## Konsequenzen

**Positiv:**
- Feed-Verkehr für einen Teilbaum-Move sinkt von ~800 000 Einträgen auf
  einen, unabhängig von der Teilbaumgrösse.
- Das `MAX_DEFERRED_TOMBSTONES`-Korrektheitsrisiko entfällt ersatzlos für
  Ordner-Moves.
- `MoveDirectory` wird durch die neue Transaktion nebenbei atomar
  (`ensureAncestors` + Rewrite + Event in einem Commit) — heute ist das ein
  Crash-Fenster.
- Kein neuer Client-Anwendungspfad — Wiederverwendung von `apply_renames`
  und `db.rename_file`, beide bereits getestet.

**Negativ:**
- Protokolländerung an ADR-0036: ein alter Client (ohne `feed_version=2`)
  bricht hart, nicht nur bei einem betroffenen Move.
- `ensureAncestors`s Zeilen-Locks werden jetzt bis zum Commit der gesamten
  Bulk-Transaktion gehalten statt Millisekunden — bei groben, selten
  kollidierenden Ancestor-Pfaden akzeptiert, aber ein realer Unterschied zu
  heute.
- Zwei Abfragequellen statt einer im meistgelesenen Endpoint des Systems;
  Merge-Logik ist ein weiterer Ort für Off-by-one-Fehler an der
  Cursor-Grenze.
- `move_events` wächst unbegrenzt (ein Datensatz pro Ordner-Move) — winzig
  gegen die 800 000 Zeilen, die es ersetzt; Pruning nicht Teil dieser
  Entscheidung.

## Referenzen

- [ADR-0036](0036-changes-feed-sequence-cursor.md) — Changes-Feed mit
  monotoner Sequenz, hier protokollseitig erweitert
- [ADR-0063](0063-verschieben-an-der-dateiidentitaet-erkennen.md) — hält die
  hier gelöste Grenze als bekannt fest
- [ADR-0060](0060-clients-aktualisieren-sich-beim-start-selbst.md) — Basis
  für die Vertretbarkeit des harten Fehlers bei fehlendem `feed_version`
