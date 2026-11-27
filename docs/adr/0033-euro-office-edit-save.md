# ADR-0033: Euro-Office Bearbeiten + Speichern über opaken Callback-Token und bestehende Konflikt-Pipeline

**Status:** Accepted
**Datum:** 2026-08-21

## Kontext

ADR-0027 lieferte read-only Vorschau für Office-Dokumente über einen
externen, OnlyOffice-API-kompatiblen Document-Server. Der nächste Slice der Euro-Office-Integration
erweitert das um Bearbeiten + Speichern für Nutzer mit Schreibzugriff:
der Document-Server rendert den Editor selbst und ruft bei jedem Speichern
eine von PrivaSync konfigurierte Callback-URL mit den neuen Bytes auf. Zwei
Fragen offen: wie wird dieser Callback-Request autorisiert (er kommt vom
Document-Server, nicht vom eingeloggten Nutzer), und wie fügt sich ein Save
in die bestehende Sync-Engine mit Versionierung/Konflikt-Handling ein, ohne
einen Sonderfall dafür zu bauen.

## Entscheidung

1. **Editability folgt der bestehenden Schreibrechte-Prüfung, keine neue
   Berechtigungsebene.** Eigener Root: Owner ist immer editierbar (Owning
   eines Roots ist bereits Schreibzugriff). Shared Mount: `editable` folgt
   `share.CanWrite` — ein Read-Only-Empfänger bleibt beim View-Modus aus
   ADR-0027, ein Schreib-Empfänger bekommt denselben editierbaren Config-Pfad
   wie ein Owner. Datei-Shares tragen laut ADR-0015 nie Schreibrecht, nur
   Verzeichnis-Shares — das gilt hier unverändert mit.

2. **Callback autorisiert über einen eigenen, opaken, PrivaSync-signierten
   Token — niemals über die JWT-Signatur des Document-Servers.** Der
   Document-Server signiert seine eigenen Requests mit seinem eigenen
   Schema; das zu verifizieren würde eine zweite Vertrauensbeziehung pro
   Document-Server-Implementierung einführen. Stattdessen trägt die an den
   Document-Server ausgegebene `editorConfig.callbackUrl` selbst einen von
   PrivaSync signierten Token (`officedoc.CallbackClaims`, HS256 mit
   `document_server.jwt_secret`) — für den Document-Server komplett opak,
   nur von PrivaSync beim Callback-Empfang geparst.

3. **Callback-Token ist stateless und trägt `server_version` als
   `BaseVersion`.** Keine serverseitige Session/TTL-Buchhaltung fürs
   Speichern nötig — Signatur + `exp`-Claim genügen. TTL bewusst 24h statt
   der 5-Minuten-Config-TTL aus ADR-0027: eine Editier-Session läuft oft
   deutlich länger als der Vorschau-Fetch, und der Document-Server ruft
   denselben Callback über die ganze Session hinweg auf.

4. **Speichern läuft durch die normale Coordinator-Upload-Pipeline mit
   `BaseVersion` als If-Match — kein Sonderfall für Office-Saves.** Ein
   sauberer Save (niemand hat währenddessen geschrieben) aktualisiert die
   Datei in-place. Ein Versions-Race (jemand hat zwischen Config-Ausgabe und
   Save geschrieben) liefert beim ersten Versuch `ConflictVersionMismatch`;
   da dieser Versuch nichts committet, wird derselbe Command exakt einmal
   mit gelöschtem If-Match wiederholt — das routet ihn durch den
   bestehenden Real-Conflict/Loser-Kopie-Pfad (ADR-0003) statt die
   gleichzeitige Änderung still zu überschreiben oder zu verwerfen.

5. **Speichern erzeugt einen Audit-Log-Eintrag (`files.office_saved`),
   konsistent zum bestehenden `files.*`-Namensschema** (vgl.
   `files.deleted`). Geloggt wird bei jedem erfolgreichen Coordinator-Aufruf,
   unabhängig davon ob er sauber durchlief oder über den Konflikt-Retry
   gelaufen ist — beides ist aus Audit-Sicht ein tatsächlicher Speichervorgang.

6. **Der Callback antwortet immer mit HTTP 200 und einem
   `{"error":0|1}`-Body**, wie vom Document-Server-Protokoll erwartet — nur
   ein strukturell ungültiger Request (fehlender/ungültiger Token, kaputtes
   JSON) bekommt einen echten 4xx. Lifecycle-Pings (Editing gestartet, ohne
   Änderung geschlossen, Save-Error, …) außerhalb von `MustSave`/
   `MustForceSave` werden nur bestätigt, nichts wird geschrieben.

## Konsequenzen

**Positiv:**

- Kein zweites Konflikt-Modell — Office-Saves und normale Uploads laufen
  durch exakt dieselbe Version-Check/Konflikt-Pipeline, verifiziert durch
  Server-Tests.
- Der Callback-Token bleibt für den Document-Server vollständig opak; ein
  Leak des `document_server.jwt_secret` auf Seiten des Document-Servers
  betrifft nur, was der Document-Server ohnehin schon sehen darf (dieselbe
  Trennung wie in ADR-0027 Punkt 3).

**Negativ:**

- Volle End-to-End-Verifikation (echtes Speichern im Editor gegen einen
  laufenden Document-Server) ist ohne Euro-Office/OnlyOffice-Instanz nicht
  automatisiert testbar — automatisierte Tests mocken den Document-Server
  (Callback-Body, Fetch der editierten Bytes). Manueller Testplan:
  1. Datei mit Schreibzugriff (eigener Root oder Schreib-Mount) im
     Editor öffnen, Änderung vornehmen, im Editor speichern → Änderung
     muss über den normalen Download-/Sync-Pfad mit neuem Inhalt abrufbar
     sein.
  2. Dieselbe Datei parallel außerhalb des Editors überschreiben, dann im
     Editor speichern → bestehende Konflikt-Behandlung (Loser-Kopie) muss
     greifen, kein stiller Datenverlust.
  3. Read-Only-Mount-Empfänger öffnet dieselbe Datei → Editor bleibt im
     View-Modus aus ADR-0027, keine Speicherfunktion sichtbar.
  4. Ein Callback mit abgelaufenem/manipuliertem Token muss abgelehnt
     werden, ohne die Datei zu verändern.
