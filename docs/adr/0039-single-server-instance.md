# ADR-0039: Genau eine Serverinstanz

**Status:** Accepted
**Datum:** 2026-08-24

## Kontext

Vier Stellen im Server setzen stillschweigend voraus, dass genau ein Prozess
läuft. Keine davon sagt das, und von aussen sieht der Aufbau aus, als wäre er
auf horizontale Skalierung ausgelegt — Outbox-Pattern, `FOR UPDATE SKIP
LOCKED`, eine Worker-Abstraktion:

1. **Download-Token-Map im Speicher** —
   [`download_token.go`](../../apps/server/internal/api/handler/download_token.go)
   hält die Einmal-Tokens für ZIP-Downloads in einer prozesslokalen `map`.
   Eine zweite Instanz kennt einen dort gemünzten Token nicht.
2. **Office-Doc-Token-Map im Speicher** —
   [`office_documents.go`](../../apps/server/internal/api/handler/office_documents.go),
   gleiches Muster für die Dokumentserver-Abholung (ADR-0027).
3. **SSE-Hub prozesslokal** — [`sse/hub.go`](../../apps/server/internal/sse/hub.go)
   verteilt Events nur an Verbindungen des eigenen Prozesses. Auch der
   Outbox-Replay publiziert ausschliesslich an den lokalen Hub, ein Client an
   Instanz B verpasst also jedes Event aus Instanz A.
4. **Der Outbox-Lock hält nicht** —
   [`outbox/repository.go`](../../apps/server/internal/outbox/repository.go)
   führt `ClaimNext` über `pool.QueryRow` aus, also ausserhalb einer
   expliziten Transaktion. Der Zeilen-Lock wird beim impliziten Commit sofort
   wieder freigegeben, lange bevor der Handler läuft und `MarkDone` greift.
   `FOR UPDATE SKIP LOCKED` ist dort wirkungslos.

Punkt 4 ist der gefährlichste, weil sein Kommentar bisher ausdrücklich das
Gegenteil behauptete („so multiple workers … can run safely"). Heute schadet
das nicht: `Worker.Run` ist eine einzelne Goroutine und `drain` arbeitet
sequenziell. Beim ersten zweiten Replica würden Outbox-Einträge doppelt
ausgeführt — sichtbar als doppelt versendete Mails.

## Entscheidung

PrivaSync läuft in **genau einer Serverinstanz**. Das ist eine bewusste
Festlegung, kein Zufall.

Begründung entlang der Anforderungen: das System ist ein selbstgehosteter
Datei-Sync für überschaubare Nutzerzahlen. Es gibt keine Anforderung an
Mehr-Instanz-Betrieb, und der Aufwand für echtes Scale-out (Token-Ablage in
der DB, SSE-Fan-out über `LISTEN/NOTIFY` oder Redis, eine korrekt gehaltene
Outbox-Transaktion) kauft ohne diese Anforderung nichts. Die Grenze ist
zudem nicht die Instanzzahl: der Server ist I/O- und speichergebunden, nicht
CPU-gebunden über einen Prozess hinaus.

Folgerichtig:

- Der irreführende `ClaimNext`-Kommentar wird korrigiert; er beschreibt jetzt
  die tatsächliche Lock-Semantik und benennt diese ADR.
- Die Deployment-Doku (`README.md`, `docker-compose.yml`) benennt die
  Einschränkung, damit niemand `--scale server=2` als unterstützt annimmt.

## Konsequenzen

**Positiv**

- Die vier Stellen bleiben so einfach, wie sie sind: keine zusätzliche
  Infrastruktur, kein Redis, keine verteilte Token-Ablage.
- Die Annahme steht schriftlich, statt in vier Dateien verstreut implizit zu
  gelten.

**Negativ / Risiken**

- Kein horizontales Skalieren und kein Zero-Downtime-Deploy durch parallel
  laufende Instanzen. Ein Neustart trennt alle SSE-Verbindungen; die Clients
  verbinden sich selbsttätig neu und holen über den Changes-Cursor
  (ADR-0036) auf, sodass keine Änderung verloren geht.
- Die Instanz ist ein Single Point of Failure. Für den Einsatzzweck
  akzeptiert; ein Container-Neustart dauert Sekunden.

## Umkehrbarkeit

Der Weg zu Variante B (Scale-out) bleibt offen und ist inkrementell: `ClaimNext`
in eine echte `tx.Begin()`-Klammer bis nach `MarkDone` legen, die beiden
Token-Maps in eine Tabelle verlagern, den SSE-Hub über `LISTEN/NOTIFY`
fan-outen. Keine dieser Änderungen ist eine Einbahnstrasse — deshalb wird sie
jetzt nicht auf Vorrat gebaut.

## Nachträge

- **2026-08-26 (ADR-0043):** Der Boot-Sweep des Chunked-Upload-Assemblers ist
  die fünfte Stelle, die von dieser Festlegung lebt. Er setzt jede Session in
  `queued`/`running` beim Start zurück und reiht sie neu ein — vollständig nur
  deshalb, weil nach einem Neustart garantiert kein anderer Prozess noch eine
  davon hält. Bei zwei Instanzen würde der Sweep der einen die laufenden
  Assemblies der anderen requeuen und dieselbe Datei doppelt committen.
