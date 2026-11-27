# ADR-0073: Browser-Downloads laufen über den Download-Manager des Browsers

**Status:** Accepted
**Datum:** 2026-10-15

## Kontext

Der Web-Client lud Einzeldateien per `fetch()` mit Bearer-Header, las den Body
mit `res.blob()` komplett in den Speicher und reichte erst danach eine
`blob:`-URL an ein `<a download>`. Bei einer Datei von 1 GB vergingen so
minutenlang ohne jede sichtbare Reaktion, der Speicherbedarf lag bei der vollen
Dateigröße, und ein abgebrochener Download ließ sich weder pausieren noch
fortsetzen. Ordner und ganze Sync-Ordner liefen bereits über eine
Einmal-Token-URL (`/download/{token}`) direkt in den Download-Manager.

## Entscheidung

**Jeder Download aus dem Web-Client geht über `/download/{token}`. Der Server
entscheidet am Pfad des Grants, was er ausliefert.**

- Zeigt der Grant auf eine Datei, wird die Datei selbst ausgeliefert (über
  `serveFileContent`, also mit Range-Unterstützung nach ADR-0047). Ein Ordner
  oder kein Pfad liefert wie bisher ein ZIP.
- Ein Datei-Grant ist **nicht** einmalig. Der Browser setzt einen pausierten
  oder abgerissenen Download mit einem Range-Request auf dieselbe URL fort.
  Nach jeder Nutzung bleibt der Grant 24 h gültig. `If-Range` gegen
  `Last-Modified` sorgt dafür, dass eine inzwischen geänderte Datei neu
  beginnt, statt zwei Versionen zusammenzustückeln.
- ZIP-Grants bleiben einmalig. Ein ZIP-Stream hat keine Länge und ist nicht
  fortsetzbar.
- Shared Mounts nutzen denselben Weg: `POST /shared-mounts/{id}/download-token`
  nimmt optional `?path=` relativ zum Mount. Die frühere Ablehnung von
  Datei-Mounts entfällt, denn es wird nichts gezippt.

Verworfen: der Media-Grant (ADR-0067) als Download-URL. Er hat dieselbe
Mehrfach-Nutzung, ist aber auf Wiedergabe zugeschnitten (inline, 15 min).

## Konsequenzen

- Der Download startet sofort mit Fortschrittsanzeige im Browser. Der Speicher
  der Seite bleibt unabhängig von der Dateigröße.
- Eine Datei-Download-URL ist bis zu 24 h nach ihrer letzten Nutzung ein
  Inhaber-Token für genau diese eine Datei, nur lesend. Sie landet in der
  Download-Liste des Browsers. Wer darauf Zugriff hat, hat die Datei ohnehin.
- Grants liegen im Speicher. Nach einem Server-Neustart lässt sich ein
  laufender Download nicht mehr fortsetzen und muss neu gestartet werden.
- `files.download()` und `sharedMounts.download()` bleiben für Vorschau,
  Editor und Thumbnails, wo die Seite den Inhalt selbst braucht.
