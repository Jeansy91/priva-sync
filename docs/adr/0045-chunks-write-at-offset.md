# ADR-0045: Chunks schreiben an ihren Offset, ein Lesedurchgang publiziert

**Status:** Superseded by ADR-0066 (nur die Adressierung `chunk_index * chunk_size`; die Offset-Schreib-Mechanik in `storage.WriteChunkAt` gilt unverändert weiter)
**Datum:** 2026-08-26

## Kontext

Ein abgeschlossener Chunk-Upload bewegte seine Bytes viermal über die Platte.
Jeder Chunk landete als eigene Datei unter `.chunks/{sessionID}/`, `AssembleChunks`
las alle wieder ein und schrieb sie in eine Staging-Kopie neben das Ziel, und
danach öffnete der Coordinator dieselbe Staging-Datei ein zweites Mal, um die
1-MiB-Blocksignaturen für den Delta-Sync zu bilden. Bei 20 GB sind das 20 GB
schreiben, 20 GB lesen, 20 GB schreiben und nochmal 20 GB lesen — 80 GB I/O für
eine Datei, die einmal ankommt.

Beide Verdopplungen sind zufällig, nicht notwendig: die Chunk-Grenzen sind bekannt
(`chunk_size` steht seit ADR-0043 auf der Session-Zeile),
und Gesamt-Hash wie Blocksignaturen entstehen aus demselben Bytestrom.

## Entscheidung

**Jeder Chunk schreibt an `offset = index * chunk_size` in eine gemeinsame
Sparse-Datei** `{dataPath}/.chunks/{sessionID}/data`
([storage.WriteChunkAt](../../apps/server/internal/storage/storage.go)). Die
Assembly-Kopie entfällt ersatzlos. Jeder Aufruf benutzt ein eigenes Handle, teilt
also keinen Dateizeiger — parallele Chunks in beliebiger Reihenfolge ergeben
dieselbe Datei.

**Der Abschluss liest genau einmal.** `FinalizeChunks` streamt die Staging-Datei
durch BLAKE3 und optional durch einen `delta.SignatureWriter`, der aus demselben
Strom die Blocksignaturen baut. Die Signaturen reisen als
`UploadCmd.BlockSignatures` zum Coordinator, der sie nur noch serialisiert statt
die Datei erneut zu öffnen. `delta.BlockSignatures` ist auf denselben Writer
umgebaut — die beiden Wege können damit nicht auseinanderlaufen.

**Die Staging-Datei bleibt ausserhalb des User-Roots.** Ihr Pfad wird rein aus der
Session-ID abgeleitet, ohne Nutzereingabe. Im User-Root wäre eine halb
hochgeladene Datei für Listing, `directory_repair` und FS-Recovery sichtbar.

**Invariante: `.chunks` und der Daten-Root liegen auf demselben Volume.** Vorher
erzwang `os.CreateTemp(filepath.Dir(absPath), …)` das implizit; jetzt ist der
finale `os.Rename` die einzige Veröffentlichung und über Volume-Grenzen hinweg
(EXDEV) unmöglich. `storage.VerifyChunkVolume` probiert den Rename beim Start mit
einer leeren Datei aus und **bricht den Start ab**, wenn er scheitert. Geprüft wird
per Probe, nicht per Device-Nummer: Bind-Mounts, Junctions und Per-User-Mounts
brechen die Invariante, ohne dass eine Device-ID das zeigt. Fail-fast, weil der
Alternativzustand ist, dass jeder grosse Upload erst nach vollständiger
Übertragung fehlschlägt.

**Lücken.** Ohne separate Chunk-Dateien kann das Storage einen fehlenden mittleren
Chunk nicht mehr an einer fehlenden Datei erkennen — eine Lücke liest sich als
Nullbytes. Die Absicherung liegt dort, wo sie ohnehin gehört: die
`received_chunks`-Bitmap lässt `complete` gar nicht erst zu, solange ein Chunk
fehlt, die Längenprüfung fängt einen fehlenden letzten Chunk, und der
Client-Hash-Vergleich prüft die Bytes selbst. Resume läuft unverändert über
dieselbe Bitmap.

Verworfen: die Blocksignaturen schon beim Chunk-Empfang zu bilden. Chunk-Grenzen
sind kein Vielfaches der Blockgrösse, die Chunks kommen ungeordnet an, und der
Gesamt-BLAKE3 lässt sich so ohnehin nicht bilden.

## Konsequenzen

**Positiv**

- 20 GB Upload: 20 GB schreiben + 20 GB lesen statt 80 GB I/O.
- Der Spitzenplatzbedarf halbiert sich — keine zweite volle Kopie mehr während
  der Assembly.
- Kein halb assemblierter Rest im User-Root nach einem Fehlversuch.
- Delta-Cache und Datei entstehen aus demselben Strom, können also nicht driften.

**Negativ**

- Neue harte Betriebs-Invariante (ein Volume), die beim Start den Server stoppt.
- Ein fehlender mittlerer Chunk ist auf Storage-Ebene nicht mehr sichtbar; die
  Bitmap ist ab jetzt die strukturelle Garantie.
- Sparse-Dateien: die belegte Grösse während des Uploads sagt nichts über den
  Fortschritt — `received_chunks` schon.
- Beim Upgrade laufende Chunk-Uploads sind verloren: ihre alten
  `chunk_000000`-Dateien passen nicht mehr, der Abschluss scheitert permanent
  mit `chunks_missing`, der Client lädt neu hoch.

## Referenzen

- ADR-0043 — asynchrones Complete, liefert `chunk_size` und die Session-Zustände
- ADR-0002 — Delta-Sync, Blockgrösse und Signaturformat
