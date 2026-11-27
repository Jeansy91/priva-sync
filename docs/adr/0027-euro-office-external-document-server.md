# ADR-0027: Euro-Office als externer, konfigurierbarer Document-Server

**Status:** Accepted
**Datum:** 2026-08-06

## Kontext

Die Euro-Office-Integration will Office-Dokumente (DOCX/XLSX/PPTX)
im Web-Dateibrowser lesbar öffnen. Rendering von Office-Formaten im Browser
ist kein Problem, das PrivaSync selbst lösen soll — es gibt dafür etablierte,
API-kompatible Document-Server (OnlyOffice Document Server, dessen
kommerzielles/EU-gehostetes Derivat "Euro-Office" u. a.). Phase 3 (dieser
Slice) liefert den ersten, read-only Slice.

## Entscheidung

1. **Externer, selbst betriebener Document-Server — kein Repo-Default, kein
   neuer docker-compose-Service.** PrivaSync bringt keinen Document-Server
   mit. Wer das Feature nutzen will, betreibt Euro-Office/OnlyOffice separat
   und trägt dessen URL + JWT-Secret in die eigene Config ein
   (`document_server.url`, `document_server.jwt_secret`,
   `apps/server/internal/config/config.go`). Begründung: ein vollwertiger
   Document-Server ist ein eigener, ressourcenhungriger Dienst mit eigenem
   Lifecycle/Update-Rhythmus — ihn optional zu lassen hält den
   Self-Hosting-Fußabdruck von PrivaSync selbst unverändert klein.

2. **Kein eingebauter Default für URL/Secret; beides-oder-nichts
   validiert beim Start.** Eine leere Konfiguration bedeutet
   `Enabled() == false` — das Feature ist dann vollständig inaktiv, es gibt
   keine Requests gegen eine falsche/leere URL. Ein halb gesetztes Config
   (nur URL oder nur Secret) ist mit hoher Wahrscheinlichkeit ein
   Tippfehler in der Env-Var und wird beim Start hart abgelehnt
   (`config.validate`), statt das Feature still im kaputten Zustand laufen
   zu lassen.

3. **Eigenes, vom App-JWT getrenntes Signing-Schema.** Der
   `document_server.jwt_secret` ist bewusst nicht `auth.jwt_secret` — die
   Editor-Config wird vom externen Document-Server selbst verifiziert, nicht
   von PrivaSync. Ein Leak des einen Secrets darf niemals das andere System
   kompromittieren. Umsetzung: eigenes Package `internal/officedoc`
   (`officedoc.BuildAndSign`), HS256, TTL 5 Minuten
   (`configTokenTTL`) — re-öffnen der Vorschau holt sich einfach eine neue
   Config statt ein wiederverwendbares Langzeit-Token zu erhalten.

4. **View-only in diesem Slice — `permissions.edit` ist hart auf `false`
   gesetzt**, nicht optional. Ko-Editing/Speichern zurück nach PrivaSync ist
   ein separates, deutlich komplexeres Folge-Issue (Callback-Handling,
   Konflikt-Strategie mit dem bestehenden Sync-Engine-Modell). Diese Phase
   liefert ausschließlich lesbares Rendern.

5. **Der Document-Server holt die Rohbytes über einen eigenen, opaken,
   kurzlebigen Fetch-Token** (`GET /office/documents/{token}`, öffentliche
   Route ohne Bearer-Auth) statt über den normalen authentifizierten
   Download-Endpunkt. Begründung: der Document-Server kann unseren
   `Authorization`-Header nicht mitschicken, wenn er die Datei serverseitig
   nachlädt. Der Token ist an genau ein Dokument (Owner/Root/Pfad) gebunden,
   TTL 5 Minuten, nicht bei erstem Zugriff verbraucht (der Document-Server
   kann während einer offenen Vorschau mehrfach nachladen — anders als der
   bestehende `downloadGrant`-Mechanismus, der Einmal-Downloads für den
   nativen Save-Dialog abdeckt).

6. **Zugriffsprüfung folgt der bestehenden Lese-Autorisierung, keine neue
   Berechtigungsebene.** `OfficeConfig` (eigener Root) prüft Root-Ownership
   wie jeder andere Root-Endpunkt; `MountOfficeConfig` (Shared Mount) nutzt
   denselben `resolveMount`-Pfad wie die übrigen Mount-Endpunkte und prüft
   **nicht** `can_write` — Lesezugriff auf den Mount reicht, weil diese
   Phase ohnehin nie schreibt.

7. **Web-seitig ein weiterer Fall in der bestehenden Preview-Auswahl**
   (`previewKindFor`, `apps/web/src/pages/files/previewKind.ts`), kein
   eigener Dateibrowser-Modus. DOCX/XLSX/PPTX bekommen den neuen
   `PreviewKind = 'office'`; ein neuer `OfficeViewerDialog` lädt die
   signierte Config und instanziiert den Document-Server über dessen
   eigenes `DocsAPI`-Skript (`{document_server_url}/web-apps/apps/api/
   documents/api.js`), das intern ein Iframe erzeugt. Ist der
   Document-Server nicht konfiguriert (Endpunkt antwortet 404) oder beim
   Laden des Skripts/der Config nicht erreichbar, fällt die Vorschau auf
   dieselbe "nicht unterstützt"-Meldung zurück wie jedes andere
   nicht-previewbare Format — kein separater, unerklärter Fehlerzustand.

## Konsequenzen

**Positiv:**

- Null Betriebsaufwand für PrivaSync selbst, solange das Feature nicht
  genutzt wird — Standard-Deployments bleiben unverändert.
- Sauber getrennte Vertrauensgrenze: ein kompromittierter Document-Server
  kann nur Dokumente lesen, für die er einen gültigen, kurzlebigen Token
  bekommen hat — nicht sich selbst beliebige Tokens ausstellen (dafür
  bräuchte er `document_server.jwt_secret`, das PrivaSync nie an den Client
  ausliefert).
- Der Preview-Layer bleibt ein einziger Erweiterungspunkt
  (`previewKindFor` + `FilePreviewLayer`) statt eines Parallel-Systems.

**Negativ:**

- Volle End-to-End-Verifikation (echtes Rendern im Editor) ist ohne einen
  laufenden Euro-Office/OnlyOffice-Server nicht automatisiert testbar.
  Automatisierte Tests mocken den Document-Server (Config-Erzeugung,
  Signatur, Autorisierung, Fallback-Verhalten); der tatsächliche
  Rendering-Rundlauf muss manuell gegen eine echte Instanz verifiziert
  werden — in dieser Arbeitsumgebung stand kein Euro-Office/OnlyOffice-
  Server zur Verfügung, daher ist dieser Schritt
  **nicht** ausgeführt worden, nur als Testplan festgehalten:
  1. Einen OnlyOffice Document Server (oder Euro-Office-Derivat) mit einem
     JWT-Secret starten, `document_server.url` + `document_server.jwt_secret`
     in der PrivaSync-Server-Config entsprechend setzen, Server neu starten.
  2. Im Web-Client eine DOCX/XLSX/PPTX-Datei im eigenen Sync-Root anklicken
     → Editor muss lesbar im Iframe erscheinen, ohne Edit-Werkzeugleiste
     (`permissions.edit == false`).
  3. Dieselbe Datei über einen Read-Only Share-Mount eines zweiten Nutzers
     öffnen → muss ebenfalls rendern; ein dritter Nutzer ohne jeden Zugriff
     auf den Pfad muss beim Endpunkt-Aufruf 404 erhalten (kein Editor).
  4. Das ausgestellte JWT decodieren (z. B. jwt.io) und prüfen: `exp` liegt
     ≈5 Minuten in der Zukunft, Payload enthält kein wiederverwendbares
     Langzeit-Token.
  5. `document_server.url`/`jwt_secret` wieder leeren, Server neu starten →
     Klick auf dieselbe Datei zeigt wieder nur die normale
     "kein Vorschau verfügbar"-Meldung, keine kaputte/leere Iframe-Anzeige.
  6. Den Document-Server stoppen, ohne die Config zu ändern → Klick auf die
     Datei muss ebenfalls auf die "nicht unterstützt"-Meldung zurückfallen
     (Skript-/Netzwerkfehler abgefangen), nicht auf einen rohen Fehlerdialog.
- Betreiber, die das Feature wollen, tragen die zusätzliche
  Betriebslast/den Lizenzaufwand eines separaten Document-Servers selbst —
  bewusst in Kauf genommen (Punkt 1).
