# ADR-0062: Ein offener Änderungs-Auftrag benennt den Pfad, nicht den Zustand

**Status:** Accepted
**Datum:** 2026-09-03

## Kontext

Scheitert das Anwenden einer **Server-Änderung** auf dem Desktop-Client, wird
sie mit ihrem **serialisierten Feed-Eintrag** in `failed_server_changes`
abgelegt und nach einer Backoff-Frist unverändert erneut abgespielt.

Zwischen dem Scheitern und dem Retry liegen Minuten, in denen der Feed
weiterläuft. Der abgelegte Eintrag altert dabei still — nichts prüft ihn, und
nichts räumt ihn weg, wenn eine spätere Änderung ihn überholt hat.

Am 01.09.2026 kostete das eine Datei ihre Eindeutigkeit:

```
14:37:21  apply change failed for '/3D-Drucker/Benchy Harbors by HRM Studios.3mf':
          CfCreatePlaceholders (next retry in 60s)
14:37:51  apply change failed for '/3D-Drucker/Benchy-Dock.3mf': …
14:37:51  applying 2 deferred tombstone(s)
14:37:51  removed locally (server deleted): /3D-Drucker/Benchy-Dock.3mf
14:37:51  removed locally (server deleted): /3D-Drucker/Benchy Harbors by HRM Studios.3mf
14:38:21  retry succeeded for '/3D-Drucker/Benchy Harbors by HRM Studios.3mf'
14:41:23  retry succeeded for '/3D-Drucker/Benchy-Dock.3mf'
```

Um 14:37:51 war der lokale Zustand korrekt. Dreissig Sekunden später hat der
Retry beide Pfade aus eingefrorenen Schnappschüssen wiederbelebt — eine davon
als Platzhalter, dessen Server-Zeile es nicht mehr gibt und der seither im
Minutentakt `hydration failed … HTTP 404 Not Found` meldet.

Der Auslöser war ein zeitweise ablehnender Cloud-Files-Provider. Der Fehler
ist aber keiner der Cloud-Files-Anbindung: **jede** Fehlerursache, die eine
Zeile in die Retry-Queue legt, erzeugt dasselbe Fenster.

## Entscheidung

**Ein offener Auftrag benennt den Pfad, nicht den Zustand.** Die Retry-Zeile
trägt keinen Feed-Eintrag mehr. Beim Retry wird der aktuelle Server-Zustand
des Pfads frisch geholt und *der* angewandt — ist der Pfad serverseitig weg,
passiert nichts, und nichts wird wiederbelebt.

Das ist der Unterschied zwischen einer Queue aus Arbeitsaufträgen und einer
Queue aus Schnappschüssen. Ein Schnappschuss ist ab dem Moment seiner Ablage
eine Behauptung über die Vergangenheit; angewandt wird er aber in der
Zukunft. Ein Auftrag, der nur den Pfad nennt, kann nicht veralten.

Verworfen: die Retry-Zeile beim Anwenden eines Tombstones mitzulöschen. Das
hätte genau diesen Vorfall verhindert und wäre billiger gewesen — deckt aber
nur Löschungen ab. Eine zwischenzeitliche *Änderung* würde weiterhin von einem
alten Schnappschuss überschrieben. Die Fehlerklasse bliebe offen, nur seltener.

**Der Zustand kommt über einen Stat-Endpoint, der eine Pfadliste annimmt** —
`POST /sync-roots/{rootID}/files/stat` und der Zwilling unter
`/shared-mounts/{shareID}`. POST statt GET, weil der Request eine bis zu
500 Einträge lange Pfadliste als JSON-Body trägt — passend zum bestehenden
Muster `GET .../files/delta` (einfacher Parameter) vs.
`POST .../files/delta/apply` (JSON-Body beschreibt die Anfrage) im selben
Router. Ein Einzel-Stat pro Auftrag skaliert nicht: am
01.09. lehnte derselbe Provider beim Start für den Root `Dokumente` 12 691
Einträge in Folge ab. Ein Root-weiter Ausfall darf keine fünfstellige Zahl von
Requests nach sich ziehen. In Blöcken zu 500 sind es 26.

## Konsequenzen

**Positiv**

- Ein Retry kann keinen überholten Zustand mehr herstellen — unabhängig
  davon, was ihn überholt hat.
- Ein Massenausfall kostet Requests in der Grössenordnung der Blockzahl, nicht
  der Dateizahl.
- Der Stat-Endpoint ist auch ausserhalb des Retry-Pfads brauchbar; bisher gibt
  es keine Möglichkeit, einen einzelnen Pfad abzufragen, ohne sein
  Elternverzeichnis zu paginieren.

**Negativ**

- Ein Retry kostet einen Request, den er vorher nicht brauchte. Retries sind
  seltene Ereignisse, aber sie treten gehäuft auf, wenn sie auftreten — genau
  deshalb die Stapelung.
- Neue API-Fläche in zwei Namensräumen, mit der üblichen Pflicht, beide gleich
  zu halten.
- Der Client kann einen Auftrag nicht mehr offline abarbeiten. Das war vorher
  auch nur scheinbar möglich: das Anwenden lädt ohnehin Bytes.

## Referenzen

- [ADR-0063](0063-verschieben-an-der-dateiidentitaet-erkennen.md) — die andere
  Hälfte desselben Vorfalls, auf der Senderseite
- `docs/glossary.md` — **Server-Änderung**, **Offener
  Server-Änderungs-Auftrag**
- Log Host A, 2026-09-01 14:37–14:44 — der oben zitierte Ablauf
