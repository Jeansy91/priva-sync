# ADR-0047: Downloads streamen über eine gestagte Temp-Datei

**Status:** Accepted
**Datum:** 2026-08-26

## Kontext

Jeder Download im Desktop-Client sammelte den vollständigen Response-Body im
Speicher, bevor irgendetwas damit geschah. Der Sync-Pfad schrieb den Puffer
anschließend auf die Platte und hashte ihn ein zweites Mal aus demselben Puffer;
der Konflikt-Pfad reichte ihn als `&[u8]` an `materialise_conflict` weiter. Für
die Dateigrößen, für die der Chunked-Upload gebaut wurde (ADR-0043, ADR-0045),
heißt das: eine 4-GB-Datei belegt 4 GB RAM, nur um auf der Platte zu landen.

Der Upload-Pfad hatte diese Umstellung längst hinter sich — Chunks schreiben an
ihren Offset, der Hash entsteht in einem Lesedurchgang. Der Download war die
verbliebene Hälfte.

Serverseitig fehlte die Voraussetzung dafür, nur einen Ausschnitt zu holen: der
Download-Handler kopierte die Datei mit `io.Copy` in den Response, `Range` wurde
nie beantwortet.

## Entscheidung

**Der Server beantwortet `Range`.** `DownloadFile` und `DownloadMountFile` gehen
über `storage.Open` (ein `*os.File`, also seekbar) und `http.ServeContent` statt
über `storage.ServeFile`
([`serveFileContent`](../../apps/server/internal/api/handler/download_serve.go)).
206, `Content-Range` und `Accept-Ranges` fallen damit an, ohne dass der Handler
Range-Arithmetik selbst führt. Die Identitäts-Header (`X-Content-Hash`,
`X-Server-Version`) setzt der Handler weiterhin vorher — sie beschreiben die
ganze Datei, auch in einer Teilantwort.

**Der Client hält keinen Body mehr im Speicher.** `download_stream` gibt einen
`DownloadStream` zurück: Header ausgewertet, Body noch auf der Leitung.
`drain_to` schreibt ihn in einen `AsyncWrite` und schreibt dabei jedes Stück den
`TransferStats` gut; `collect` sammelt ihn nur dort ein, wo etwas anderes als die
Dateigröße die Schranke ist — in der Praxis ein Range-Fenster. Der gepufferte
Pfad (`read_body_counted`, `DOWNLOAD_PREALLOC_CAP`) entfällt ersatzlos.

**Ein Durchgang erzeugt Datei und Hash.** `stage_download` schreibt den Strom
durch einen `HashingWriter` in eine Temp-Datei neben dem Ziel; der BLAKE3 läuft
auf demselben Bytestrom mit. Ergebnis ist ein `StagedDownload` mit Pfad, Hash und
Größe. Das frühere Schreiben-dann-nochmal-Hashen entfällt.

**Der Temp-Pfad ist eindeutig und endet weiter auf `.tmp`.** Beides ist
erzwungen: `.tmp` ist das, worauf Local-Walk und Watcher überspringen — ohne das
staged der Watcher das Halbfabrikat als Upload. Eindeutig, weil
`dest.with_extension("tmp")` für `notes.txt` und `notes.md` im selben Verzeichnis
kollidiert; solange die Datei nur einen Moment existierte, war das theoretisch,
bei einem minutenlangen Multi-GB-Download ist es real.

**`StagedDownload` besitzt seine Temp-Datei.** `Drop` löscht sie, solange sie
nicht publiziert wurde, und `publish` räumt sie auch bei fehlgeschlagenem Rename
weg. Ein abgebrochener Download hinterlässt damit nichts, was der nächste Walk
findet.

**Der Konflikt-Pfad bekommt den Gewinner gestagt, nicht als Bytes.**
`materialise_conflict` nimmt einen `StagedDownload` und gibt dem Aufrufer Hash
und Größe in den Persist-Callback. Die crash-relevante Reihenfolge bleibt drin:
Der Overwrite von `local_path` ist der Rename in `publish`, nicht das Befüllen
der Temp-Datei — die Loser-Kopie entsteht davor, wie bisher.

Verworfen: den Konflikt-Pfad gepuffert zu lassen. Er ist genau der Fall mit dem
höchsten Speicher- und I/O-Druck (zusätzlich wird eine Loser-Kopie geschrieben)
und hätte den kompletten In-Memory-Pfad nur für sich am Leben gehalten.

Verworfen: `materialise_conflict` einen Writer-Callback zu geben. Das zöge
Netzwerk-I/O und Retry-Semantik in die Choreografie, machte die heute synchrone
Funktion `async` und koppelte ihre Tests an die Implementierung.

## Konsequenzen

**Positiv**

- Der Speicherbedarf eines Downloads ist unabhängig von der Dateigröße.
- Ein Durchgang statt zweier über die Bytes: schreiben und hashen fallen zusammen.
- Zwei Downloads ins selbe Verzeichnis können sich nicht mehr über die
  Temp-Datei in die Quere kommen.
- Kein verwaistes `.tmp` mehr nach einem Abbruch.
- `Range` steht als Server-Fähigkeit auch für andere Clients bereit (Web-Preview,
  WebDAV-Adapter).

**Negativ**

- `http.ServeContent` setzt `Content-Type` per Endung statt per Sniffing des
  ersten Writes — für einzelne Endungen kann sich der Header ändern.
- Der Handler beantwortet jetzt auch `If-Modified-Since` / `If-Range` und kann
  damit `304` liefern, wo vorher immer `200` kam. Korrektes HTTP, aber neues
  Verhalten für alles, was über einen Browser-Cache geht.
- Der Sync-Pfad lädt über einen `ApiSnapshot` statt über den gesperrten Client.
  Das gibt den API-Lock während des Transfers frei, heißt aber auch, dass das
  Bearer-Token zum Startzeitpunkt eingefroren ist; der Auth-Retry liegt wie
  bisher beim Aufrufer.
- Ein Download belegt jetzt Plattenplatz, bevor er fertig ist — vorher belegte er
  stattdessen genauso viel RAM.

**Offen**

- `DownloadFile` hebt die absolute `WriteTimeout` des Servers nicht per
  `ResponseController` auf. Das ist Vorverhalten und durch diese Änderung
  unverändert, wird aber bei sehr großen Dateien zur Grenze.

## Referenzen

- ADR-0045 — Chunks schreiben an ihren Offset; dieselbe Ein-Durchgang-Regel für
  den Upload
- ADR-0024 — Live-only-Fläche des Sync-Executors
- ADR-0048 — Hash-Provenienz und Versions-Pin der Cloud-Filter-Hydration
