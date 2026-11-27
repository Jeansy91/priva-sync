# ADR-0002: Delta-Sync mit BLAKE3 + rsync-Block-Hashes

**Status:** Accepted — Server-Seite + Desktop-Client-Matching umgesetzt
**Datum:** 2026-04-30

## Kontext

Große Dateien (Office-Dokumente, VM-Images, PSDs) ändern sich häufig nur
partiell. Voll-Upload bei jedem Save belastet Heim-Upstreams und Quota
unnötig. Ein Block-Storage / CAS-Ansatz fällt aus, weil der Server seine
Dateien als Klartext-Spiegelung im Host-FS halten muss — Chunks im
DB-Backend widersprechen dem Designziel.

## Entscheidung

**rsync-ähnliches Delta-Protokoll**, Server speichert immer die vollständige
rekonstruierte Datei.

- Hash: **BLAKE3** (parallel, deutlich schneller als SHA-256,
  kryptografisch sicher) — sowohl für Content- als auch Block-Hashes.
- Blockgröße: 1 MB, gleitend.
- Schwelle: Delta nur bei `size > 1 MB` und wenn die Datei serverseitig
  bereits existiert. Sonst Full-Upload.
- Ablauf: Client `POST /sync/delta-request` → Server liefert Block-Hashes →
  Client überträgt nur geänderte Blöcke + Rekonstruktions-Plan → Server
  schreibt vollständige Datei + aktualisiert `files`.

## Konsequenzen

**Positiv:**
- Bytes auf der Leitung skalieren mit Diff-Größe, nicht Dateigröße.
- Server-Speicher bleibt einfach: ein File pro Pfad, kein Chunk-GC.
- BLAKE3 schluckt Millionen-File-Hashes ohne Bottleneck zu werden.

**Negativ:**
- Server muss Block-Hashes on-the-fly berechnen (oder cachen in
  `block_hashes JSONB`) — Lese-Latenz bei sehr großen Dateien.
  - **Umgesetzt:** `UploadFile` ist der einzige byte-ändernde
    Chokepoint (Full-Upload *und* Delta-Apply) und persistiert die
    Block-Signaturen ab Größe `MinDeltaSize` als JSON in `files.block_hashes`.
    `delta-request` liest den Cache und fällt auf On-the-fly zurück, wenn die
    Spalte NULL ist (Altbestand vor der Migration). Copy trägt den Cache mit,
    Move/Rename behalten dieselbe Zeile — der Cache bleibt automatisch
    konsistent. Ein Cache-Miss schlägt nie den Upload fehl (degradiert zu NULL).
  - **Offen (Messung):** Ob der Cache die Schreib-Last (ein zusätzlicher
    Staging-Read pro Upload >1 MiB) gegenüber der eingesparten Lese-Latenz
    rechtfertigt, ist noch nicht real gemessen. Die fehlerfreie Fallback-Logik
    erlaubt, den Cache jederzeit folgenlos abzuschalten.
- Rekonstruktion braucht den vollständigen Plan vor dem Schreiben; bricht
  der Upload mittendrin ab, fällt das gesamte Delta zurück.
- Kein Deduplizieren über Dateien hinweg.
- Der Desktop-Client difft für Owned-Roots ab 1 MiB. Das Matching
  streamt die Datei durch einen begrenzten Gleitpuffer (auch chunk-große
  Dateien werden in beschränktem Speicher diffd) und bricht ab, sobald die
  geänderte (Literal-)Nutzlast die Chunked-Schwelle überschreitet — dann ist
  ein normaler Full-/Chunked-Upload vergleichbar. Den letzten kürzeren
  Server-Block matcht der Client nicht (rsync-Standard) — minimaler
  Bandbreitenverlust, Korrektheit unberührt.
