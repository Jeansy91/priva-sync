# ADR-0048: Hash-Provenienz und Versions-Pin der Cloud-Filter-Hydration

**Status:** Accepted
**Datum:** 2026-08-26

## Kontext

Der FETCH_DATA-Callback der Cloud-Filter-Hydration lud die **gesamte** Datei
herunter, um daraus `[RequiredFileOffset .. +RequiredLength]` herauszuschneiden.
Explorer fordert eine Datei fensterweise an, jedes Fenster als eigener Request —
ein sequentielles Lesen einer 4-GB-Datei zog sie also mehrfach vollständig durchs
Netz. Anschließend schrieb der Callback `blake3::hash(&data)` per `upsert_file`
in die lokale DB-Zeile.

Mit einem Range-Request (ADR-0047) trifft nur noch das Fenster ein. Aus dem
Puffer lässt sich kein Datei-Hash mehr bilden — was die Frage aufwirft, was diese
Spalten überhaupt bedeuten sollen.

## Entscheidung

**Für einen Placeholder trägt die lokale Zeile die Behauptung des Servers.**
`SyncEngine::apply_server_file_entry` schreibt den Server-Hash bei der Erzeugung
des Placeholders in `content_hash` **und** `server_hash`, mit Status `synced` —
vor jedem Byte auf der Platte. Das ist gewollt: der Placeholder *ist* semantisch
in sync, die Bytes sind ein Hydrationsdetail. Jede Korrektheitsentscheidung hasht
ohnehin die lokale Datei frisch und vergleicht dagegen (`walk`, `transfer`,
`events`); `content_hash` ist advisory, nie autoritativ.

**Der Hydrations-Callback schreibt deshalb gar keinen Hash mehr.** Die Zeile ist
bereits korrekt; der bisherige `blake3::hash(&data)` rechnete nur nach, was sie
schon behauptete. Der Write war zusätzlich schädlich: `upsert_file` überschreibt
`sync_status` bedingungslos, eine Hydration setzte damit eine offene
`conflict`-Zeile auf `synced` zurück und verwaiste ihre `.conflict.`-Kopie. Und
`chrono::Utc::now()` als mtime klobberte die Server-mtime, wodurch der Fast-Path
im Local-Walk bei jeder hydrierten Datei verfehlte.

**Der Server-Hash wandert nicht in die DB.** `resolve_conflict(pending=false)`
promotet `content_hash` → `server_hash`. Eine server-behauptete Zahl in
`content_hash` würde dort zu bestätigter Wahrheit über lokale Bytes gewaschen,
die niemand geprüft hat. Der `X-Content-Hash`-Header bleibt deshalb ungenutzt.

**Stattdessen pinnt jede Hydration auf eine Serverversion.** Der Callback
vergleicht `X-Server-Version` der Range-Antwort gegen die `server_version` der
Placeholder-Zeile und bricht bei Abweichung mit
`STATUS_CLOUD_FILE_NOT_IN_SYNC` ab. Ohne das setzt sich eine Datei, die während
der Hydration serverseitig neu geschrieben wird, aus **zwei Versionen** zusammen
— und gilt anschließend als `synced`. Eine abgebrochene Hydration ist
wiederholbar, eine zerrissene Datei nicht.

Damit es etwas zu vergleichen gibt, legt `apply_server_file_entry` die
Placeholder-Zeile über `upsert_synced_with_version` an statt über `upsert_file`,
das `server_version` nicht setzt. Fehlt die Version auf einer der beiden Seiten
— `-1` als Nicht-bekannt-Sentinel, oder ein Endpunkt ohne den Header, etwa
Shared Mounts —, läuft die Hydration durch: eine Prüfung, die nicht möglich ist,
darf nicht als Fehlschlag gelten.

**Ein 200 auf einen Range-Request wird lokal zurechtgeschnitten.** Ein Server,
der den Header ignoriert, liefert die ganze Datei; `hydration_window` schneidet
dann hier. Korrekt, nur nicht billig — weshalb der Range-Request überhaupt
existiert.

Verworfen: den Hash erst bei Voll-Hydration zu schreiben (Explorer garantiert
kein Offset-0-über-die-volle-Länge, und im Trefferfall stünde derselbe Wert schon
da). Verworfen: die fertige Datei lokal nachzuhashen — ein zusätzlicher voller
Lesedurchgang, ohne Aufhänger, weil die CF-API kein „vollständig hydriert"-Signal
liefert.

## Konsequenzen

**Positiv**

- Ein Explorer-Zugriff auf den Anfang einer großen Datei überträgt nur dieses
  Fenster statt der ganzen Datei, und das pro Fenster statt pro Fenster erneut
  vollständig.
- Kein DB-Write mehr im Callback: eine offene Konflikt-Zeile überlebt eine
  Hydration, und die Server-mtime bleibt stehen.
- Eine Datei kann nicht mehr aus zwei Serverversionen zusammengesetzt werden.
- Die Bedeutung der Hash-Spalten ist einmal festgeschrieben statt pro Aufrufer
  neu erraten.

**Negativ**

- Neue Fehlerklasse in Explorer: eine Hydration kann mit „nicht synchron"
  abbrechen, wenn der Server dazwischen schreibt. Der nächste Reconcile-Zyklus
  frischt den Placeholder auf, ein erneuter Zugriff hydriert dann sauber.
- Der Pin greift nicht für Shared Mounts und nicht für Placeholder, die vor
  dieser Änderung angelegt wurden (`server_version = -1`) — dort bleibt das alte
  Risiko bestehen, bis die Zeile einmal neu geschrieben wurde.
- Ein Server ohne Range-Unterstützung fällt auf lokales Zurechtschneiden zurück,
  also auf das alte Übertragungsverhalten.

## Referenzen

- ADR-0047 — Downloads streamen; liefert `download_stream` samt Range und
  `X-Server-Version`
- ADR-0003 — Konflikt-Loser-Kopie, deren Zeile der bisherige Write zurücksetzte
- ADR-0011 — Shared Mounts, deren Changes-Feed keine `server_version` führt
