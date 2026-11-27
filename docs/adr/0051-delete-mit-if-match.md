# ADR-0051: Löschen mit If-Match

**Status:** Accepted
**Datum:** 2026-08-27

## Kontext

Upload und Move tragen seit jeher `If-Match` (RFC 7232) und melden eine
Versionskollision als HTTP 409, den der Client als Konflikt auflöst. `DELETE`
tat das nicht: weder Client noch Handler kannten den Header, `DeleteCmd` hatte
kein Feld dafür.

Ein Löschen war damit ein Blind-Write. Client A löscht offline, Client B ändert
dieselbe Datei, A kommt online — die Änderung von B verschwindet, ohne dass
irgendwo ein Konflikt sichtbar wird. Zwei Dinge entschärften das, ohne es
aufzuheben: der serverseitige Papierkorb verhindert echten Byte-Verlust, und
`KeepLocalOverDelete` rettet bei B die lokale Datei, falls B die Änderung
zufällig noch ungesynct hält. Für alle anderen Clients ist die Datei schlicht
weg.

Die Asymmetrie war der eigentliche Fehler: dieselbe Klasse von Rennen wird bei
Upload und Move als Konflikt behandelt, beim Löschen ignoriert.

## Entscheidung

**`DELETE /files` akzeptiert `If-Match` mit derselben Semantik wie Upload und
Move.** Weicht die Server-Version von der genannten ab, antwortet der Server
mit 409 und der aktuellen `server_version` — in derselben Body-Form wie der
Upload-Konflikt, damit der Client nur einen Shape kennen muss.

**Der Header bleibt optional.** Ohne ihn löscht der Server unbedingt, exakt wie
bisher. Das hält ältere Clients und die Web-UI funktionsfähig; die Garantie
bekommt, wer eine Version nennen kann. `DeleteCmd.IfMatch` ist deshalb
`*int64`, nicht `int64`.

**Der Desktop-Client sendet die `server_version` der `pending_delete`-Zeile.**
Sie überlebt das Staging: `upsert_file` lässt die Spalte unangetastet, ein
`stage_delete` überschreibt sie also nicht. Unbekannte Version (`-1`) heißt
"nichts zu behaupten" und sendet keinen Header.

**Bei 409 gewinnt der Server.** Der Client verwirft die lokale Löschabsicht
(`delete_file_record`), statt sie zu wiederholen, und emittiert `sync_conflict`.
Die neue Server-Version kommt über den regulären Pull nach. Das ist die
konservative Richtung: eine Löschung, die der Nutzer erneut auslösen kann, wiegt
leichter als eine fremde Änderung, die niemand wiederherstellen kann.

## Konsequenzen

- Ein Delete kann jetzt fehlschlagen, wo es vorher immer durchging. Der
  Client behandelt das als Konflikt, nicht als Fehler — kein Retry-Loop.
- Der Papierkorb bleibt die zweite Verteidigungslinie, ist aber nicht mehr die
  einzige.
- Verzeichnis-Deletes (`deleteDirectorySubtree`) bleiben unbedingt. Eine
  sinnvolle Version für einen ganzen Teilbaum gibt es nicht, und der
  Einzeldatei-Pfad deckt den Fall ab, um den es geht.
