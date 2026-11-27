# ADR-0066: Chunk-Grösse passt sich pro Chunk an, Adressierung wird Byte-Range

**Status:** Accepted (supersedes ADR-0045 für Chunk-Adressierung)
**Datum:** 2026-09-04

## Kontext

Die Chunk-Grösse für Uploads war eine feste Konstante (5 MiB), vom Server bei
`init_chunked_upload` einmal festgelegt und für die ganze Session unverändert
übernommen. Auf einer schnellen Leitung erzeugt das unnötig viele Requests, auf
einer langsamen oder instabilen ist ein 5-MiB-Chunk teuer erneut zu
übertragen, wenn er kurz vor Abschluss abbricht.

ADR-0045 verankert `offset = chunk_index * chunk_size` mit **einem**
`chunk_size`-Wert pro Session — Voraussetzung dafür ist, dass sich die Grösse
innerhalb einer Session nicht ändert. Eine Anpassung, die tatsächlich auf
gemessene Uploaddauer reagiert (statt nur auf die nächste Session zu wirken),
bricht dieses Invariant zwangsläufig.

## Entscheidung

**Der Client wählt pro Chunk seine Grösse**, nach NextCloud-Vorbild
(`propagateuploadng.cpp`): `targetSize = chunkSize/2 + predictedGoodSize/2`
mit `predictedGoodSize = (chunkSize * targetDuration) / uploadTime` — eine
gedämpfte Anpassung auf Basis der zuletzt gemessenen Chunk-Uploaddauer, ein
Zustandswert (letzte Grösse + Dauer), keine separate Durchsatz-Mittelung.
Minimal-/Maximalgrösse und Zieldauer sind Client-Konstanten
([chunked.rs](../../apps/desktop/src-tauri/src/sync/chunked.rs)), der Server
kennt sie nicht — er hat keine Sicht auf die Netzwerkbedingungen des Clients
und könnte sie nicht sinnvoll überschreiben. Bei Resume wird die Chunklänge
zusätzlich auf die verbleibende Lücke gekappt (`min(target, gap)`), damit ein
Chunk nicht über das Ende der Datei hinausschreibt.

**Adressierung wird Byte-Range statt Index.** `PUT
.../uploads/{uploadID}/chunks/{offset}` ersetzt `.../chunks/{chunkIndex}` —
die Länge ergibt sich aus dem Body (`Content-Length`), wie schon heute. Ein
`Content-Range`-Header wäre der naheliegende HTTP-Standard gewesen, verlangt
aber `total_size` in jedem Chunk-Request zur Gegenprüfung gegen die
Session-Zeile — ein Feld, das nur durch Client-Bugs abweichen kann und sonst
nichts beiträgt. Der Server begrenzt die Chunklänge weiterhin serverseitig
über `MaxBytesReader`, jetzt mit einer festen, grosszügigen Obergrenze statt
`sess.ChunkSize` — unabhängig davon, was der Client für sich selbst gewählt
hat.

**Fortschritt läuft über Byte-Abdeckung, nicht über eine Chunk-Bitmap.**
`received_chunks INT[]` + `total_chunks` weicht einer `int8multirange`-Spalte
(Postgres 16, pgx v5 unterstützt den Typ nativ). Ankunft eines Chunks ist eine
Vereinigung (`received_ranges + int8multirange(int8range(offset,
offset+length))`), die fehlenden Bereiche für Status/Resume sind die Differenz
zur Gesamtgrösse (`int8multirange(int8range(0, total_size)) -
received_ranges`) — beides ein SQL-Operator, kein Merge-Code in Go.
`storage.WriteChunkAt(sessionID, offset, reader)` (ADR-0045) bleibt
unverändert, es nahm den Offset schon immer explizit entgegen.

**Keine explizite Overlap-Validierung.** Ein wiederholter, identischer Range
ist harmlos (gleiche Bytes, gleicher Offset, `int8multirange`-Vereinigung
verschmilzt ihn ohne Doppelzählung). Ein Client-Bug, der einen falschen, aber
überlappenden Range schreibt, wird vom bestehenden Content-Hash-Abgleich beim
Complete gefangen (`ErrContentHashMismatch`) — eine zweite Prüfung beim
Schreiben wäre redundant für einen Fall, der bereits fail-closed ist.

Verworfen: Anpassung nur zwischen Sessions (Server passt die Konstante für
die nächste Session an, ohne Protokolländerung) — deutlich kleinerer Eingriff,
aber bewusst nicht gewählt, weil die Anpassung dann erst nach einem
kompletten Upload wirkt statt innerhalb eines grossen Transfers.

## Konsequenzen

**Positiv**

- Chunk-Grösse reagiert auf die tatsächliche Leitung innerhalb eines
  einzelnen Uploads, nicht erst beim nächsten.
- Byte-Range-Adressierung ist die direktere Abbildung variabler Grössen als
  eine nachträglich künstlich konstant gehaltene `chunk_size`.
- `int8multirange` erspart Merge-/Overlap-Code in Go vollständig.

**Negativ**

- **Breaking Change am Chunk-PUT-Vertrag**: Pfad-Segment wird zum Offset,
  `chunk_size`/`total_chunks`/`received_chunks` verschwinden aus
  Init-/Status-Antwort zugunsten von Byte-Bereichen. Client und Server ziehen
  im selben Slice um (gleiches Muster wie ADR-0043).
- Laufende Chunk-Uploads beim Upgrade sind verloren — wie schon bei ADR-0045
  akzeptiert; der Client lädt neu hoch.
- Ein Storage-seitiger Hinweis auf einen fehlenden mittleren Chunk (früher:
  fehlende Chunk-Datei) existiert weiterhin nicht; die Byte-Abdeckung ist ab
  jetzt die alleinige strukturelle Garantie, wie schon in ADR-0045 beschrieben.

## Referenzen

- ADR-0045 — vorheriges Adressierungsschema (`chunk_index * chunk_size`),
  für die Adressierung hiermit abgelöst; die Offset-Schreib-Mechanik in
  `storage.WriteChunkAt` gilt unverändert weiter
- ADR-0043 — asynchrones Complete, Content-Hash-Abgleich als bestehender
  Fail-Closed-Mechanismus
