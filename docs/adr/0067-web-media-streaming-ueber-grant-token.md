# ADR-0067: Web-Audio/Video streamt über einen Media-Grant statt Voll-Download

**Status:** Accepted
**Datum:** 2026-09-05

## Kontext

`AudioPlayer.tsx` und `ImageGalleryDialog.tsx`/`GalleryVideoPlayer.tsx` laden
Audio- und Video-Dateien im Web-Client aktuell komplett als Blob (Bearer-authed
`fetch`), bevor überhaupt Wiedergabe beginnen kann. Bei großen Dateien (Filme,
Hörbücher) heißt das: lange Wartezeit vor dem ersten Bild/Ton — obwohl der
Server `Range` längst beantwortet (ADR-0047, `serveFileContent` über
`http.ServeContent`).

Grund für den Blob-Umweg: Auth läuft über `Authorization: Bearer` im Header.
Ein natives `<video src="...">`/`<audio src="...">` kann diesen Header nicht
mitschicken — der Browser fragt die URL direkt und ohne Custom-Header ab.

Nebenbefund: `id3Cover.extractCover` liest ohnehin nur die ersten
`min(size, 1MB)` Bytes (ID3v2-Tag sitzt am Dateianfang), wird aber am
kompletten heruntergeladenen Blob aufgerufen — eine Ineffizienz, die exakt aus
demselben Grund entstand (der Blob war "sowieso schon da") und mit demselben
Umbau mitgelöst wird.

## Entscheidung

1. **Neuer Grant-Typ, analog `officeDocGrant` (ADR-0027).** Opaker Token,
   pfadgebunden (Owner/Root/Path bzw. Mount-Scope wie ADR-0010), **mehrfach
   nutzbar** (nicht wie `downloadGrant`, dessen Einmal-Verbrauch für einen
   Player ungeeignet wäre, der beim Seeken/Puffern viele einzelne
   Range-Requests feuert). Die öffentliche Route (keine Bearer-Middleware)
   liefert über `serveFileContent` aus — also Range-fähig — statt über
   `storage.ServeFile` wie beim Office-Grant.

2. **Kurze TTL (15–20 min) mit proaktivem clientseitigem Renewal**, nicht
   Office' 5 min und nicht eine lange TTL über Stunden. Ein gemeinsamer Hook
   erneuert den Token im Hintergrund bei ~70–80 % der Laufzeit, merkt sich
   `currentTime` + Play-Zustand und tauscht `src` ohne hörbaren/sichtbaren
   Abriss; ein reaktiver Fang von 401/404 bleibt als Fallback. Begründung: der
   Grant ist ein vom App-JWT unabhängiges Bearer-Äquivalent — Logout,
   Share-Widerruf oder Passwortänderung invalidieren ihn nicht. Eine TTL über
   Stunden hieße, dieses Äquivalent für die gesamte Spieldauer eines Films im
   DOM/Devtools/Browser-Verlauf liegen zu lassen. Kurze TTL + Renewal hält das
   Fenster klein, ohne Wiedergabe bei langen Medien abreißen zu lassen.

3. **Cover-Art-Extraktion wechselt auf Range-Fetch der ersten 1 MB** über den
   bestehenden Bearer-authed Download-Endpunkt, statt auf den (nach Punkt 1
   entfallenden) Voll-Blob zu warten. Direkte Folge der Umstellung, kein
   separates Ticket. `extractCover` darf nicht annehmen, dass exakt 1 MB
   zurückkommt — ein Proxy, der `Range` ignoriert, liefert `200` mit dem
   vollen Body, und das muss weiter funktionieren.

4. **Scope: nur Web, nur `previewKindFor`-Kinds `audio`/`video`.** Der
   Desktop-Client bleibt unverändert — er arbeitet bereits lokal gegen
   gestagte Dateien (ADR-0047 betraf dort den Download-Pfad, nicht die
   Wiedergabe).

5. **Zugriffsprüfung folgt dem bestehenden Lesezugriffs-Muster**, keine neue
   Berechtigungsebene — wie ADR-0027 Punkt 6: Mount-Scope prüft nur Leserecht,
   nicht `can_write`.

6. **Kein explizites Revoke-on-close.** Der Grant lebt bis zum TTL-Ablauf; ein
   zusätzlicher Invalidierungs-Endpunkt für "Player geschlossen" wäre bei einer
   ohnehin kurzen TTL zusätzliche Komplexität ohne spürbaren Sicherheitsgewinn.

## Konsequenzen

**Positiv**

- Wiedergabe beginnt, sobald der erste Range-Chunk da ist, statt nach dem
  kompletten Download — der eigentliche Zweck dieser Änderung.
- Cover-Art-Extraktion lädt nur noch 1 MB statt der ganzen Datei, unabhängig
  von der Dateigröße (FLAC, Hörbücher).
- Kein neuer Berechtigungsmechanismus — reine Wiederverwendung des
  ADR-0027-Musters für ein zweites "Browser kann keinen Custom-Header
  mitschicken"-Problem.

**Negativ / Offen**

- Der Grant lebt wie `officeDocGrant` in einer In-Memory-Map desselben
  Prozesses. Bei einem Multi-Instance-Deployment ohne Sticky-Sessions kann ein
  auf Instanz A erneuerter Token auf Instanz B mit 404 scheitern — bestehende
  Einschränkung von ADR-0027, durch diese ADR nicht neu eingeführt, aber nicht
  behoben.
- `http.ServeContent` setzt `Content-Type` per Dateiendung, nicht per Sniffing
  (bereits als Negativpunkt in ADR-0047 festgehalten, gilt hier unverändert).

## Referenzen

- ADR-0027 — Vorbild für Grant-Typ, Mehrfachnutzung, öffentliche Fetch-Route
- ADR-0047 — Server-seitige Range-Fähigkeit (`serveFileContent`), die dieser
  ADR erstmals für Web-Clients nutzbar macht
- ADR-0010 — Shared-Mount-Scope für Lesezugriffsprüfung
