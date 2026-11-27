# ADR-0072: Lese- und Schreib-Timeout sind Idle-Timeouts

**Status:** Accepted
**Datum:** 2026-10-15

## Kontext

API- und WebDAV-Server setzen `ReadTimeout: 30s` und `WriteTimeout: 120s`. Go
wendet beide als absolute Fristen auf die gesamte Anfrage bzw. Antwort an,
egal ob noch Bytes fließen. Jede Antwort, die länger als 120 s braucht, reißt
mitten im Body ab. Bei einer 1,2-GB-Datei über eine 75-Mbit/s-Leitung war das
bei jedem Versuch der Fall, kurz vor dem Ende. Genauso bricht jeder
Request-Body nach 30 s ab, z. B. ein großer WebDAV-PUT. Für SSE und den
Chunk-Upload haben die Handler die Fristen bisher selbst aufgehoben. Dieses
Muster hatte bei den über zehn Download-Pfaden nie jemand nachgezogen.

Dazu kommt ein stillerer Effekt: Nach dem Lesen des Bodys liest der Server im
Hintergrund weiter auf der Verbindung. Läuft die Lesefrist ab, bricht er den
Request-Context ab. Alles, was beim Streamen Zeilen mit `r.Context()` nachlädt
(die ZIP-Downloads), bricht dann nach 30 s ab.

## Entscheidung

**`httpx.IdleTimeouts` umschließt beide Server als äußerster Handler.
Kein Handler fasst Fristen selbst an.**

- Vor jedem Schreiben wird die Schreibfrist auf jetzt + `WriteTimeout` gesetzt,
  vor jedem Body-Read die Lesefrist auf jetzt + `ReadTimeout`. Abgeschnitten
  wird nur noch eine Gegenstelle, die hängt, nie eine lange Übertragung.
- Ist der Body zu Ende gelesen oder gar nicht vorhanden, wird die Lesefrist
  aufgehoben. So bleibt der Request-Context erhalten.
- Die Header bleiben unter der absoluten `ReadTimeout`. Gegen Slowloris ändert
  sich nichts.
- `ReadFrom` bleibt erhalten und gibt in Häppchen von 4 MiB an sendfile weiter.
  Vor jedem Häppchen wird die Frist neu gesetzt. Das trägt bei 120 s noch einen
  Client mit 35 KB/s. `Hijack` reicht der Wrapper mit durch, denn chis Logger
  leitet `ReadFrom` nur weiter, wenn der innere Writer auch `Hijacker` ist.

## Konsequenzen

- Neue Routen müssen sich um Fristen nicht kümmern.
- Ein Client, der langsam, aber stetig liest oder sendet, wird nicht mehr
  begrenzt. Größenlimits kommen weiterhin von `MaxBytesReader`, nicht von
  Fristen.
