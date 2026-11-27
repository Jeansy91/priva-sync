# ADR-0058: Der Walk-Checkpoint steht auf jeder Ebene, nicht nur auf der obersten

**Status:** Accepted
**Datum:** 2026-08-28

## Kontext

[ADR-0037](0037-local-reconcile-scale-redesign.md) hat dem Voll-Walk einen
persistierten Checkpoint gegeben: den letzten **vollständig abgeschlossenen
Top-Level-Eintrag**. Ein Abbruch sollte damit dort weitermachen, wo der
vorherige Lauf aufgehört hat.

Das funktioniert genau so lange, wie ein Root viele Top-Level-Einträge hat. Auf
einem Root, dessen Inhalt unter *einem* Ordner liegt — ein ganzes Laufwerk mit
allem unter `\Persönlich` —, wird dieser eine Eintrag nie fertig, bevor der
Client das nächste Mal neu startet. Der Checkpoint rückt also nie vor, und jeder
Start beginnt denselben Teilbaum von vorn.

Sichtbar wurde das als Beobachtung aus dem Feld: nach jedem Update steht die
Anzahl der zu synchronisierenden Dateien auf ihrem alten Wert und sinkt nur
langsam durch die Uploads — statt wieder zu steigen, weil der Walk neue Dateien
findet. Er findet keine: er läuft stat für stat durch Verzeichnisse, deren
Dateien längst in der Datenbank stehen, und erreicht unerkundetes Gebiet erst
nach Stunden wieder.

## Entscheidung

**Der Checkpoint ist der Pfad des letzten abgeschlossenen Eintrags, gleich
welcher Tiefe** — Datei oder Verzeichnis. Drei Dinge gehören dazu:

**1. Deterministische Reihenfolge auf jeder Ebene.** Bisher war nur die
Top-Level-Liste sortiert; `reconcile_directory` iterierte `read_dir` in
Dateisystem-Reihenfolge. Ein Resume-Punkt in beliebiger Tiefe verlangt, dass
„alles, was davor sortiert, ist fertig" auf jeder Ebene gilt. Sortiert wird nach
demselben NFC-normalisierten `rel`-String, gegen den der Checkpoint verglichen
wird. Der Speicherbedarf bleibt eine Verzeichnisbreite — die Schranke, die der
Walk ohnehin hatte.

**2. Drei Antworten statt zwei beim Wiederaufsetzen.** `Skip` für einen
abgeschlossenen Eintrag, `Walk` für alles ab dem Checkpoint — und neu
`Descend` für das Verzeichnis, *in* dem der vorherige Lauf stehengeblieben ist:
es wird betreten, aber innerhalb wird weiter vorgespult. Erkannt am
Präfix (`checkpoint` beginnt mit `rel + "/"`), geprüft **vor** dem
`<=`-Vergleich, damit `/aa` nicht mit `/a/b` verwechselt wird.

**3. Gedrosselt geschrieben.** Ein Checkpoint pro Eintrag wäre ein
Config-Write pro Datei. Geschrieben wird höchstens alle zwei Sekunden; ein
abgeschlossener Top-Level-Eintrag schreibt immer, weil er der wertvollste
Resume-Punkt ist. Ein verpasster Write kostet den nächsten Lauf ein paar
Sekunden wiederholtes Stat-en, nie Korrektheit.

Die Invariante, auf der alles ruht: **jeder Eintrag, der auf seiner Ebene an
oder vor dem Checkpoint sortiert, wurde vollständig verarbeitet.** Der Walk hält
sie, indem er Geschwister sortiert besucht und ausschließlich nach Abschluss
notiert.

## Considered Options

- **Beim Alten bleiben und nur den zweiten Voll-Pass aus
  [ADR-0056](0056-walk-entkoppelt-uploads-laufen-mit.md) nutzen.** Der läuft
  ohnehin nach jedem Resume und findet alles — aber erst, nachdem der erste Pass
  denselben Teilbaum noch einmal abgelaufen ist. Das ist genau die Wartezeit,
  die auffiel.
- **Den kompletten Pfad-Stack persistieren** statt eines einzelnen Pfades.
  Erlaubt exaktes Wiederaufsetzen ohne Sortierannahme, führt aber einen zweiten
  persistenten Zustand ein, der selbst abbruchsicher sein müsste. Ein Pfad plus
  Sortierordnung trägt dieselbe Information.
- **Checkpoint nach Zeit statt nach Position** (alle N Minuten neu anfangen)
  — löst das Problem nicht, es verschiebt es nur.

## Konsequenzen

- Ein Neustart mitten im Walk erreicht innerhalb von Sekunden wieder
  unerkundetes Gebiet. Die Anzahl der offenen Dateien steigt danach wieder, statt
  bis zum Aufholen des Walks nur zu sinken.
- Jede Verzeichnisebene wird jetzt sortiert eingelesen. Das ist eine
  Allokation pro Verzeichnis statt eines Streaming-Iterators — vernachlässigbar
  gegen das, was pro Eintrag ohnehin passiert (`metadata`, DB-Abfrage), und die
  Speicherschranke ändert sich nicht.
- Der zweite Voll-Pass nach einem Resume (ADR-0056) bleibt unverändert nötig:
  er deckt Änderungen ab, die im übersprungenen Bereich entstanden, während der
  Client unten war. Der feinere Checkpoint macht ihn nicht überflüssig, nur den
  ersten Pass nützlicher.
