# ADR-0085: Identische Uploads sind kein Konflikt

**Status:** Accepted
**Datum:** 2026-11-11

## Kontext

Ein Upload ohne `If-Match` auf einen belegten Pfad wurde immer als echter
Konflikt behandelt (ADR-0003), auch wenn die Bytes mit der Server-Datei
übereinstimmten. Ein Client lädt blind hoch, sobald er die `server_version`
einer Datei nicht kennt: nach einem Re-Add des Sync-Roots, bei einem
verlorenen Upload-Response oder einem Retry. Bei einem Konto mit
Fotobeständen entstanden so hunderte Konflikte (438 von 575 offenen waren
byte-identisch zur aktuellen Datei), jeweils mit Kopie, Quota-Verbrauch und
Benachrichtigung. Die Auflösung ist für den Nutzer reine Handarbeit ohne
Entscheidungsgehalt.

## Entscheidung

Der Koordinator prüft vor der Konfliktentscheidung, ob der Upload dieselben
Bytes liefert wie die Live-Datei (gleiche Größe, gleicher serverseitig beim
Schreiben berechneter BLAKE3-Hash, Hash vorhanden). Dann gilt:

- kein Konflikt, keine Konflikt-Zeile, keine Loser-Kopie, keine Mail;
- die Staging-Datei wird entfernt;
- die Antwort ist die bestehende Datei mit ihrer aktuellen `server_version`,
  sodass der Client sie als synchron übernimmt.

Der Hash stammt aus dem Schreibvorgang des Servers, nicht aus einer Angabe des
Clients. Fehlt er oder weicht die Größe ab, gilt weiter ADR-0003.

## Verworfen

- **Dubletten nur beim Auflösen erkennen:** Der Konflikt, die Kopie und die
  Mail entstehen trotzdem.
- **Nur im Client abfangen (adopt-Pfad):** Deckt Clients ohne Index, verlorene
  Responses und Retries nicht ab; die Garantie gehört auf die Server-Seite.
- **Änderungsdatum mitvergleichen:** Ein abweichendes mtime bei gleichem
  Inhalt ist keine zu entscheidende Wahl.

## Konsequenzen

- Identische Dateien erzeugen keinen Konflikt mehr, unabhängig vom Client.
- Der mtime der Server-Datei bleibt unverändert, auch wenn der Client einen
  anderen meldet.
