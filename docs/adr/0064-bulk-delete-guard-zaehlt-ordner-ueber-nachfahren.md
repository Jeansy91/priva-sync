# ADR-0064: Bulk-Delete-Guard zählt Ordner über Nachfahren, löst über Ratio ODER absolute Schwelle aus

**Status:** Accepted
**Datum:** 2026-09-04

## Kontext

Der Bulk-Delete-Guard vergleicht `count_pending_deletes` gegen
`count_tracked_files` und pausiert den Root, sobald das Verhältnis 30 %
übersteigt (mindestens `BULK_DELETE_MIN_FILES` Dateien). `stage_delete`
schreibt für eine gelöschte Datei wie für einen gelöschten Ordner dieselbe
eine `pending_delete`-Zeile — es gibt keine Kaskade in die lokale DB, der
Teilbaum wird erst serverseitig aufgelöst
([files_mutate.go:189-191](../../apps/server/internal/api/handler/files_mutate.go#L189-L191)).
Eine Ordner-Löschung zählt damit immer als 1, unabhängig von der Grösse des
Teilbaums: ein gelöschter Ordner mit 400 000 Dateien ergibt `1 / 400000` und
bleibt für den Guard unsichtbar. Beobachtet am 03.09.2026: eine
Ordner-Umbenennung ging als Löschung durch, der komplette Teilbaum
landete kommentarlos im Papierkorb, ohne Rückfrage.

Die 30-%-Ratio hat ausserdem ein zweites, unabhängiges Problem, sobald sie
Ordner korrekt mitzählt: sie skaliert in die falsche Richtung. Ein
100-Dateien-Root braucht 30 Löschungen zum Auslösen, ein Millionen-Root
300 000 — ausgerechnet dort, wo ein Fehler am meisten kostet, greift der
Guard am spätesten oder praktisch nie.

## Entscheidung

**Eine `pending_delete`-Zeile auf einem Verzeichnis zählt sich selbst plus
ihre im lokalen Index bekannten Nachfahren.** Die stehen alle in
`local_files` unter demselben Pfadpräfix; die Zählung ist ein `COUNT` mit
`LIKE`-Präfix (wiederverwendet `escape_like`, dasselbe Muster wie
`move_subtree`) und kein Dateisystem-Walk. Gezählt werden alle Zeilen unter
dem Präfix, Dateien wie Unterordner — dieselbe Definition, die
`count_tracked_files` für den Nenner schon verwendet; sonst würde ein
ordnerlastiger Teilbaum die Ratio künstlich verzerren.

**Der Guard löst aus, wenn eine von zwei Bedingungen zutrifft: Ratio über
30 %, oder `delete_count` über `BULK_DELETE_ABSOLUTE_THRESHOLD` (5000).** Die
Ratio bleibt, weil sie kleine Roots schützt, bei denen eine absolute Zahl nie
erreicht wird. Die absolute Schwelle schliesst die Millionen-Root-Seite. Der
konkrete Wert ist ein Richtwert, keine Messung — er soll über jedem normalen
Massen-Umbenennen liegen und unter dem, was ein sehr grosser Root an einem
Tag legitim löscht.

**Verworfen: eine mildere, ordnerbezogene Rückfrage statt Auto-Pause des
ganzen Roots.** Der Client kann „der Nutzer hat gelöscht" nicht von „etwas
hat gelöscht" unterscheiden, und genau der zweite Fall ist der, für den der
Guard existiert — eine schwächere Reaktion würde den Fall verfehlen, der am
meisten kostet.

Die Warn-/Notify-Texte (Rust-Log, OS-Notification, Desktop-Dialog) zeigen
seitdem nur noch Rohzahlen (`delete_count`/`total_count`), kein Prozent mehr:
bei einem Trigger über die absolute Schwelle auf einem Millionen-Root wäre
die Ratio winzig und ein angezeigtes „0 %" irreführend, obwohl Tausende
Dateien betroffen sind.

## Referenzen

- `docs/glossary.md` — **Bulk-Delete-Guard**
- [transfer.rs](../../apps/desktop/src-tauri/src/sync/transfer.rs) — Guard-Logik, `bulk_delete_ratio`
- [files.rs](../../apps/desktop/src-tauri/src/db/files.rs) — `count_pending_deletes`, `escape_like`
