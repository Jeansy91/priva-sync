# ADR-0056: Der Voll-Walk läuft entkoppelt, Uploads laufen mit — und ein Abbruch verliert nichts

**Status:** Accepted
**Datum:** 2026-08-28

## Kontext

[ADR-0037](0037-local-reconcile-scale-redesign.md) hat den lokalen Voll-Walk
gechunkt, gecheckpointet und aus der Engine-Loop heraus in `spawn_blocking`
verlegt. Was dabei stehen blieb: `sync_cycle` **wartet** auf den Walk, bevor es
zu `process_uploads` weitergeht. Auf einem Root mit Millionen Dateien heißt das,
dass das erste Byte erst rausgeht, wenn das letzte Verzeichnis gelesen ist —
Stunden, in denen die Upload-Queue bereits vollständig gestaged und komplett
untätig ist. Discovery (plattengebunden) und Transfer (netzgebunden) wechseln
sich ab, statt sich zu überlappen.

Drei weitere Stellen desselben Musters lagen auf genau diesem Pfad und hätten
jede Entkopplung sofort wieder aufgefressen:

- `process_uploads` lädt mit `list_pending` die **komplette** Pending-Menge pro
  Zyklus — beim Erstsync ist das der ganze Root.
- Der Bulk-Delete-Guard zählt seinen Nenner über `list_all_files(..).len()`,
  lädt also den ganzen Root, um eine Zahl zu bilden.
- `overall_status` liest jeden Pending-Datensatz des Systems, um zwei Booleans
  daraus abzuleiten.

Und die Abbruchsicherheit hatte eine echte Lücke. Der Checkpoint speichert den
letzten **vollständig abgeschlossenen** Top-Level-Eintrag; ein Resume
überspringt alles darunter. Genau das macht ein Resume billig — aber ein
Checkpoint existiert ausschließlich, wenn ein Lauf *nicht* fertig wurde
(Kill, App-Quit). Danach war der Client eine Zeit lang **unten**, ohne Watcher.
Eine Datei, die in dieser Zeit in einem übersprungenen Teilbaum entstand, sah
niemand: der tote Watcher nicht und der überspringende Pass auch nicht. Sie kam
erst mit dem nächsten Sicherheitsnetz-Lauf (20 Minuten) zurück — und nur, weil
der zufällig ohne Checkpoint startet.

## Entscheidung

**1. Der Walk wird abgesetzt, nicht abgewartet.** `spawn_reconcile_local`
beansprucht den Overlap-Guard (`active_walks`) **synchron** und spawnt dann den
Walk als eigenen Task. Der Zyklus geht sofort weiter zu `process_uploads`. Der
Walk stößt nach **jedem abgeschlossenen Top-Level-Teilbaum** `sync_now` an, so
dass ein frischer Zyklus hochkommt und hochlädt, was gerade gestaged wurde,
während der Walk weiterläuft. `Notify` hält genau ein Permit — Anstoßen pro
Teilbaum kostet nichts, wenn schon ein Zyklus läuft.

**2. Ein abgebrochener Walk wird zweimal gelaufen, nicht einmal.**
`run_chunked_walk` schleift über `walk_once`, solange ein Pass aus einem
Checkpoint resumte. Der resumende Pass läuft zuerst — er erreicht den noch
ungelaufenen Teil des Roots (und dessen Uploads) am schnellsten —, danach folgt
**ein voller Pass von oben** mit geleertem Checkpoint. Der zweite Pass ist für
alles Synchrone billig (mtime + size stimmen, nichts wird neu gehasht) und ist
das, was aus „macht da weiter, wo es aufgehört hat" ein „keine Datei geht
verloren, egal wobei die App gestorben ist" macht.

**3. Der Walk respektiert `server_initiated_removes`.** Bisher galt die
Unterdrückung nur für den Watcher, weil Walk und Server-Pull nie gleichzeitig
liefen. Jetzt tun sie es: der Walk kann ein Verzeichnis genau in dem Moment
lesen, in dem der Pull eine Datei daraus entfernt hat. Ohne die Prüfung stagt er
ein `pending_delete` und schickt die Löschung an den Server zurück — bei der
Gegenseite eines Renames wäre das eine Löschung der Datei unter ihrem neuen
Namen. Der Walk **konsumiert** den Eintrag nicht (das darf nur der Watcher-Event,
für den er geschrieben wurde), er liest ihn.

**4. Die Upload-Queue wird batchweise gelesen** (`list_pending_batch`,
`UPLOAD_BATCH_LIMIT = 2000`). Ein voller Batch stößt am Ende selbst wieder
`sync_now` an, damit der Abfluss nicht bis zum 30-Sekunden-Poll steht. Der Batch
begrenzt den Speicher, nicht den Durchsatz.

**5. Der Bulk-Delete-Guard zählt in SQL, nicht im Batch.** Zähler
(`count_pending_deletes`) und Nenner (`count_tracked_files`) kommen aus der
Datenbank. Aus dem Batch gezählt würde eine Welle von 400.000 Löschungen als
2.000 Zeilen gegen den ganzen Root erscheinen, die Schwelle nie reißen — und der
Root würde in stillen Raten gelöscht. Dieselbe Umstellung macht
`overall_status` (`pending_summary_all`) und das Settle-Gate (`has_pending`)
zu Aggregatabfragen.

**6. „Alles erledigt" gilt nicht, solange ein Walk läuft.**
`settle_directory_status` (Explorer-Ordnerstatus, ADR-0052) fragt zusätzlich
`walk_in_flight`. Eine leere Queue zwischen zwei Chunks ist eine Lücke, kein
Ende.

## Considered Options

- **Verzahnt statt entkoppelt** — der Walk ruft zwischen zwei Chunks selbst
  `process_uploads`. Startet Uploads ebenfalls nach Sekunden statt nach Stunden
  und braucht keinen neuen Nebenläufigkeits-Vertrag, überlappt Platte und Netz
  aber nicht: beide warten weiter abwechselnd aufeinander. Verworfen zugunsten
  der echten Überlappung; die Kosten dafür sind die Punkte 3–6 dieser
  Entscheidung.
- **Resume nach einem Kill ganz verbieten** (Checkpoint nur prozesslokal gültig)
  — schließt dieselbe Lücke wie Punkt 2, wirft aber bei jedem Kill die gesamte
  bereits gelaufene Arbeit weg. Auf langsamem Storage ist genau das der Grund,
  aus dem der Checkpoint überhaupt existiert.
- **Den zweiten Pass auf die übersprungenen Teilbäume beschränken** — wäre
  billiger, verlangt aber, den übersprungenen Bereich exakt zu buchführen und zu
  persistieren. Ein voller Pass ist gegen unveränderte Dateien ohnehin billig
  (kein Hashing) und braucht keinen zweiten Zustand, der selbst wieder
  abbruchsicher sein müsste.

## Konsequenzen

- Erstsync auf großen Roots lädt hoch, während er noch entdeckt. Der Nutzer
  sieht Fortschritt in Sekunden statt in Stunden.
- Ein `kill -9` mitten im Walk kostet nach dem Neustart einen zusätzlichen
  vollen (aber hash-freien) Pass — und garantiert dafür Vollständigkeit.
- Der Walk läuft jetzt parallel zu Upload, Download und Placeholder-Pass
  desselben Roots. Der Overlap-Guard verhindert weiterhin zwei Walk-Ketten pro
  Root; alles andere teilt sich die SQLite-Mutex wie bisher die Watcher- und
  Transferpfade.
- Ein beim App-Quit abgesetzter Walk-Task wird einfach fallengelassen. Er
  hinterlässt einen Checkpoint — und damit exakt den Fall, den Punkt 2 abdeckt.
