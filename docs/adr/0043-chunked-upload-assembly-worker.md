# ADR-0043: Complete nimmt an, ein eigener Worker assembliert

**Status:** Accepted
**Datum:** 2026-08-26

## Kontext

`POST .../uploads/{id}/complete` erledigte die gesamte Schwerarbeit im Request:
alle Chunks lesen, in eine Staging-Datei kopieren, BLAKE3 hashen, committen
([storage.AssembleChunks](../../apps/server/internal/storage/storage.go)). Bei
einer mehrere Gigabyte grossen Datei sind das Minuten — mehr, als irgendeine
Schicht auf dem Weg zulässt: der Reverse-Proxy gibt nach ~100 s auf,
`WriteTimeout` liegt bei 120 s, und ein Client-Pflaster von 600 s verschob das
Problem nur. Beobachtetes Symptom bei einer Arma-`data.pak`: 62 Versuche, jedes
Mal `complete chunked upload: HTTP 504 Gateway Timeout`, obwohl jeder Chunk
längst auf dem Server lag.

Schlimmer als der Fehlschlag ist, was der 504 abschneidet: er cancelt den
Request-Context **mitten im Commit**. Die Transaktion des Coordinators wird
abgebrochen, während die Staging-Datei bereits umbenannt sein kann.

## Entscheidung

Complete **nimmt die Arbeit nur an** und antwortet `202`. Assemblieren, Prüfen
und Committen läuft in einem eigenen In-Process-Worker-Pool
([`internal/assembler`](../../apps/server/internal/assembler)).

**Zustandsmaschine auf der Session-Zeile.** `open → queued → running →
completed | failed`, jeder Übergang ein einzelnes bedingtes `UPDATE`
([repository/upload.go](../../apps/server/internal/repository/upload.go)):
`MarkQueued` gilt nur aus `open`, `ClaimSession` nur aus `queued`. Zwei
gleichzeitige Completes können damit nicht beide dieselbe Datei an einen Worker
geben, und ein wiederholtes Complete liest den Zustand statt Arbeit zu
verdoppeln. Die Zeile ist gleichzeitig der Ergebnis-Record und
trägt jetzt zusätzlich `attempts` und `last_error_code`.

**Eigener Worker-Pool, nicht die Outbox.** `Worker.Run` ist eine einzelne
Goroutine und `drain` läuft strikt sequenziell
([outbox/worker.go](../../apps/server/internal/outbox/worker.go)) — eine
achtminütige Assembly würde dort jedes SSE, jede Mail und jeden Rename aller
Nutzer blockieren; ausserdem kennt die Outbox keinen terminalen Fehlerkanal.
Auch nicht `jobs.Registry`: name-gekeyed, Singleton pro Name, kein Payload. Der
Pool hat zwei Worker und ein Limit von einem Job pro Nutzer — die Arbeit ist
plattengebunden, mehr Worker machen dieselbe Platte nur langsamer und
vervielfachen den gleichzeitig belegten Staging-Platz; das Per-User-Limit
verhindert, dass eine Warteschlange grosser Dateien alle anderen aushungert.

**Der Worker läuft mit eigenem Context**, nicht mit dem des Requests
(`context.Background()` plus eigenes Timeout). Genau das war die zweite Hälfte
des 504-Schadens.

**Status statt zweiter Endpunkt.** Der bestehende `GET .../status` meldet
zusätzlich `status` und — terminal — `result` bzw. `error`. Mit `?wait=30s`
hält er den Request bis zum Zustandswechsel: der Normalfall bleibt damit
quasi-synchron und bleibt trotzdem unter jeder Proxy-Grenze. Kein neues
SSE-Event; `file_uploaded` publiziert der Coordinator ohnehin durabel und
taugt als Nudge.

**Permanent vs. transient.** Ein fehlender Chunk, eine falsche Grösse, ein
Hash-Mismatch, ein vollen Quota sind endgültig — dieselben Bytes ergeben
dieselbe Antwort, und ein Retry kostet nur ein weiteres Vollstlesen. Ein
I/O-Fehler geht zurück in die Queue, maximal dreimal; `attempts` zählt auch
abgebrochene Versuche mit, damit eine Session den Server nicht endlos in einen
Neustart-Zyklus ziehen kann.

**Boot-Sweep als Recovery.** Beim Start werden alle Sessions in `queued` oder
`running` zurück auf `queued` gesetzt und neu eingereiht. Das ist vollständig,
weil genau eine Serverinstanz läuft (ADR-0039) — nach einem Neustart hält
niemand mehr etwas. Der Session-Purge lässt in-flight-Sessions unangetastet,
sonst löscht er einem laufenden Commit die Zeile unter den Händen weg.

Verworfen: ein zweiter Endpunkt neben `/status` über derselben Zeile
(zwei Wahrheiten über einen Zustand), und ein SSE-Event als Hauptweg (der
Client müsste dann trotzdem pollen, wenn er die Verbindung verpasst hat).

## Konsequenzen

**Positiv**

- Ein mehrere GB grosser Upload läuft durch, unabhängig von jedem
  Proxy-Timeout.
- Kein Commit wird mehr von einem abgelaufenen Request gecancelt.
- Ein Complete ist beliebig oft wiederholbar, ohne Arbeit zu verdoppeln.
- Deterministische Fehler enden mit einer Aussage statt in einer Endlosschleife.

**Negativ**

- **Breaking Change am API-Vertrag**: Complete antwortet `202` statt `200`. Ein
  Client, der nur die alte Form kennt, kann das Ergebnis nicht mehr lesen.
  Client und Server ziehen im selben Slice um.
- Der Client wartet jetzt über Long-Poll und braucht dafür eine eigene
  Warte-Schleife samt Budget (eine Stunde pro Versuch; die Session lebt 24 h,
  der normale Transfer-Retry nimmt das Warten danach wieder auf).
- Der Fortschritt einer Assembly ist nach aussen grobkörnig: `queued` und
  `running`, kein Prozentwert.
- ADR-0039 bekommt eine fünfte abhängige Stelle.

## Referenzen

- ADR-0039 — genau eine Serverinstanz, Grundlage des Boot-Sweeps
- ADR-0022 — Coordinator als Unit of Work; der Commit im Worker nutzt denselben
  transportfreien Pfad wie WebDAV
- ADR-0029 — schmale Rollen-Interfaces (`SessionStore`, `Storage`, `Committer`)
