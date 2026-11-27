# Deployment

Betrieb von PrivaSync mit Docker Compose: Konfiguration, Reverse-Proxy, WebDAV und
Office-Vorschau. Die Kurzanleitung steht im [README](../README.md#quick-start).

## Docker Compose

`docker compose up -d` startet Server und PostgreSQL. Die Beispieldatei
[`docker-compose.yml`](../docker-compose.yml) baut das Image lokal (`build:`). Für den
Betrieb mit dem veröffentlichten Image ersetzt du den `build:`-Block des Dienstes `server`
durch

```yaml
image: ghcr.io/jeansy91/privasync-server:latest
```

Statt `latest` lässt sich eine Version pinnen (`ghcr.io/jeansy91/privasync-server:<version>`).
Alle Versionen stehen unter [Releases](https://github.com/Jeansy91/priva-sync/releases).

Beim ersten Start auf einem frischen Verzeichnis gehören `./data` und `./config` dem
Container-Nutzer (fest UID/GID 10001, ADR-0034):

```bash
mkdir -p ./data ./config && chown -R 10001:10001 ./data ./config
```

## Konfiguration

Konfiguration wahlweise per gemounteter `./config/config.yaml` (`./config:/config:ro`,
Vorlage: [`apps/server/config.example.yaml`](../apps/server/config.example.yaml)) oder per
Env-Vars.

**Env-Var-Format:** `PRIVASYNC_<SECTION>__<FIELD>` — **ein** Unterstrich nach
`PRIVASYNC`, doppelter zwischen den Ebenen. Beispiel: `server.base_url` →
`PRIVASYNC_SERVER__BASE_URL`, `database.url` → `PRIVASYNC_DATABASE__URL`. Env
gewinnt vor der config-Datei.

Pflicht-Vars via `.env` (Vorlage: [`.env.example`](../.env.example)): `DB_PASSWORD`,
`JWT_SECRET` (≥ 32 Zeichen, z. B. `openssl rand -hex 32`). Optional: `BASE_URL`,
`CORS_ORIGINS`, `TRUSTED_PROXIES`, SMTP-Vars — siehe `docker-compose.yml`.

## Genau eine Serverinstanz

`--scale server=2` wird nicht unterstützt: Download- und Office-Tokens liegen im
Prozessspeicher, der SSE-Hub ist prozesslokal, und die Outbox verlässt sich darauf, dass
nur ein Worker zieht. Eine zweite Instanz liefert kaputte Downloads, fehlende
Live-Updates und doppelt versendete Mails. Begründung und Weg zurück:
[ADR-0039](adr/0039-single-server-instance.md).

## Reverse-Proxy

**Hinter einem Reverse-Proxy `TRUSTED_PROXIES` setzen** (kommagetrennte CIDRs oder
Adressen). Bleibt es leer, ignoriert der Server jeden `X-Forwarded-For`/`X-Real-IP`-Header
und sieht alle Nutzer unter der Proxy-Adresse — Rate-Limit, Login-Lockout und Audit-Log
verlieren dann ihre Aussagekraft. Ohne Proxy leer lassen: dort wäre der Header frei
wählbar und würde genau diese Kontrollen aushebeln.

Betreibe PrivaSync nur über HTTPS (z. B. Caddy oder Nginx vor dem Server) und setze
`BASE_URL` und `CORS_ORIGINS` auf die öffentliche URL, etwa `https://sync.example.com`.

## WebDAV (Port 8081)

Zugriff mit rclone, dem Datei-Explorer oder anderen WebDAV-Clients. **Das
Kontopasswort funktioniert dort nicht.** Basic-Auth kann keinen zweiten Faktor
abfragen — würde das Kontopasswort gelten, wäre ein Konto mit aktivem TOTP über
diesen Port mit dem Passwort allein übernehmbar ([ADR-0040](adr/0040-webdav-app-passwords.md)).

Stattdessen unter *Einstellungen → Sicherheit → Geräte-Passwörter* ein
Passwort pro Gerät erzeugen. Es wird genau einmal angezeigt, ist einzeln
widerrufbar, und der Server speichert nur seinen Hash.

Der Desktop-Client nutzt weiterhin seinen normalen Bearer-Token und ist nicht
betroffen.

## Office-Vorschau (optional, ADR-0027)

Rendern und (wo das Format es zulässt) Bearbeiten von Office-Dokumenten —
OOXML, ODF und Alt-Binärformate, siehe ADR-0049 — über einen **extern betriebenen**,
OnlyOffice-API-kompatiblen Document-Server (Euro-Office/OnlyOffice) — nicht im
Repo enthalten, eigener Container/Dienst. Zwei Vars, beide leer = Feature aus:

```
DOCUMENT_SERVER_URL=https://office.example.com        # -> PRIVASYNC_DOCUMENT_SERVER__URL
DOCUMENT_SERVER_JWT_SECRET=<gleiches Secret wie im Document-Server>
```

Drei Stolpersteine (häufigste Fehlkonfiguration):

1. **`document_server.url` ist die Basis-URL**, *ohne* Pfad. Der Web-Client
   hängt `/web-apps/apps/api/documents/api.js` selbst an. Trägt man den vollen
   api.js-Pfad ein, wird er doppelt → 404 → stumme "nicht unterstützt"-Meldung.
2. **`server.base_url` muss vom Document-Server erreichbar sein** — nicht vom
   Browser. Der Document-Server holt die Rohbytes *selbst* darüber
   (`{base_url}/office/documents/{token}`). In Docker also der Container-/LAN-/
   Public-Host, unter dem der Document-Server den PrivaSync-Server sieht —
   niemals `localhost`, wenn der Document-Server woanders läuft.
3. **`document_server.jwt_secret` == JWT-Secret des Document-Servers**, sonst
   lehnt dieser die Editor-Config ab. Bewusst getrennt von `auth.jwt_secret`.

Kurz: `document_server.url` = Browser-Blickwinkel, `server.base_url` =
Document-Server-Blickwinkel. Die beiden zu verwechseln ist der Standardfehler.

## Clients verteilen

Die Desktop-Clients erscheinen bei jedem Release als Assets des GitHub-Releases. Der Server
verteilt keine Binaries mehr selbst: Er bietet den Clients die zu seiner Version passende
Client-Version an und verweist den Updater direkt auf die GitHub-Release-Assets (ADR-0088).
Es gibt dafür nichts zu konfigurieren.

Automatische Updates funktionieren nur, wenn das GitHub-Repository öffentlich ist, weil die
Downloads anonym erfolgen. Der Server-Host braucht dafür keinen Zugriff auf GitHub, wohl aber
jeder Client. Details: [`releasing.md`](releasing.md).

### Aufräumen nach dem Update von einer Version mit Release-Verwaltung

Frühere Versionen legten hochgeladene oder importierte Client-Releases unter
`<data_path>/releases/` ab (bzw. unter dem Pfad aus `storage.releases_path` /
`PRIVASYNC_STORAGE__RELEASES_PATH`). Der Server nutzt dieses Verzeichnis nicht mehr und löscht
es nicht selbst. Es kann einmalig von Hand entfernt werden, z. B. `rm -r ./data/releases`.
Die Config-Schlüssel `release_source.*` und `storage.releases_path` werden ignoriert und können
aus der Config entfallen. Eine Datenbank-Migration ist nicht nötig.
