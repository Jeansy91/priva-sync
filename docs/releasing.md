# Releases

Ein Release hebt die Version aller Client-Manifeste an, committet und pusht auf `main`.
Alles Weitere erledigt die CI in einem einzigen Lauf (ADR-0054): Gates, Tag, Build und
Publish.

## Ablauf

1. Sicherstellen, dass der Arbeitsbaum sauber ist (`git status` ohne Änderungen).
2. Release auslösen:

   ```bash
   make release VERSION=x.y.z
   ```

   Das Target prüft das Semver-Format und den sauberen Baum, ruft
   `scripts/bump-version.sh x.y.z` auf, committet `chore: release x.y.z` und pusht
   `HEAD` nach `origin`.
3. Die CI fährt alle Gates. Sind sie grün, setzt sie den Tag `vx.y.z`, baut die Artefakte
   und veröffentlicht sie im selben Lauf. Ein von Hand gesetzter Tag baut nichts
   (ADR-0053, ADR-0054, ADR-0055).

### Reihenfolge der Jobs nach den Gates

1. `tag-release` setzt den Tag.
2. `desktop-linux`, `desktop-windows` und `desktop-macos` (Matrix `aarch64-apple-darwin`,
   `x86_64-apple-darwin`) bauen parallel und signieren das Updater-Artefakt mit
   `TAURI_SIGNING_PRIVATE_KEY`.
3. `publish-client-release` legt das GitHub-Release `vx.y.z` mit den benannten Assets an.
4. `server-image` erzeugt aus den `.sig`-Dateien das Manifest
   (`bash scripts/build-client-manifest.sh <version> <sig-dir>`), baut danach das Image und
   pusht es. Das Image entsteht zuletzt, weil seine Signaturen aus den fertigen Clients
   stammen.

Pull-Request-Läufe bauen das Server-Image nur als Smoke-Build (`sec-trivy`), mit dem
leeren, eingecheckten Manifest.

### Asset-Namen

| Plattform (Updater-Target) | Asset | Zweck |
| --- | --- | --- |
| `windows-x86_64` | `privasync-windows-x86_64-setup.exe` (+ `.sig`) | Installer und Update |
| `linux-x86_64` | `privasync-linux-x86_64.AppImage` (+ `.sig`) | Download und Update |
| `darwin-aarch64` | `privasync-darwin-aarch64.dmg` | Erstinstallation |
| `darwin-aarch64` | `privasync-darwin-aarch64.app.tar.gz` (+ `.sig`) | Update |
| `darwin-x86_64` | `privasync-darwin-x86_64.dmg` | Erstinstallation |
| `darwin-x86_64` | `privasync-darwin-x86_64.app.tar.gz` (+ `.sig`) | Update |

Die Namen sind ein Vertrag zwischen `publish-client-release` und
`scripts/build-client-manifest.sh`; wer einen ändert, ändert beide.

`make release` pusht. Vorher also bewusst entscheiden, ob der Stand wirklich raus soll.

## Was `scripts/bump-version.sh` ändert

Das Skript setzt die Version in:

- `apps/desktop/package.json`
- `apps/desktop/src-tauri/tauri.conf.json`
- `apps/desktop/src-tauri/Cargo.toml`
- `apps/web/package.json`
- `package.json` im Root (falls vorhanden)

Zusätzlich zieht es den Selbstverweis von `privasync-desktop` in
`apps/desktop/src-tauri/Cargo.lock` nach (`cargo update --workspace --precise`). Ohne
diesen Schritt bricht `cargo llvm-cov --locked` in der CI ab. Fehlt `cargo`, muss das
Lockfile von Hand aktualisiert werden.

## Signing & Trust

Der Desktop-Client wird mit einem Ed25519-Schlüssel signiert, damit der eingebaute
Tauri-Updater nur Builds aus dieser Pipeline akzeptiert.

### Public Key

`apps/desktop/src-tauri/tauri.conf.json` → `plugins.updater.pubkey`. Öffentlich, wird mit
jedem Build ausgeliefert.

### Private Key

- **Generierung:** `pnpm tauri signer generate --ci -p "$TAURI_SIGNING_PRIVATE_KEY_PASSWORD" -w ~/.tauri/privasync.key`
- **Speicherort:** `~/.tauri/privasync.key` (lokal, nicht im Repo, via `.gitignore`
  ausgeschlossen)
- **Passphrase:** in `.env` als `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`; Backup im
  Passwort-Manager
- **CI-Secrets (GitHub Actions):** `TAURI_SIGNING_PRIVATE_KEY` (Dateiinhalt) und
  `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`
- **Backup:** verschlüsselte Offline-Kopie (z. B. USB-Stick und Passwort-Manager). Ohne Key
  sind keine signierten Updates mehr möglich.

### Recovery bei Key-Verlust

Der Public Key ist im bereits ausgelieferten Client hart verdrahtet. Ein neues Schlüsselpaar
macht alle bestehenden Installationen updater-tot.

1. Neues Schlüsselpaar generieren (`pnpm tauri signer generate`).
2. Neuen Public Key in `tauri.conf.json` eintragen.
3. Major-Version-Bump (z. B. `0.x → 1.0`), das markiert den Bruch klar.
4. In den Release-Notes dokumentieren: „Manuelle Neuinstallation nötig".
5. Neues Installer-Paket ohne Updater-Signatur-Check verteilen (Direkt-Download).

### Trust-Warnungen

PrivaSync nutzt **keine** kostenpflichtigen Code-Signing-Zertifikate. Erwartete
OS-Warnungen:

- **Windows SmartScreen:** „Der Computer wurde durch Windows geschützt." → „Weitere
  Informationen" → „Trotzdem ausführen". Die Reputation baut sich nach genug Downloads auf.
- **macOS Gatekeeper:** „App kann nicht geöffnet werden, da Entwickler nicht verifiziert."
  → Rechtsklick auf die App → „Öffnen" → bestätigen. Alternativ unter
  Systemeinstellungen → Datenschutz freigeben. Die macOS-Builds der CI sind nur ad-hoc signiert, nicht mit einem
  Apple-Developer-ID-Zertifikat signiert oder notarisiert.

Der Ed25519-Updater-Signatur-Check greift davon unabhängig: Jedes Update wird kryptografisch
verifiziert, bevor es installiert wird.

## Client-Updates über den Server

Der Server bietet Clients nur die Client-Version an, die seiner eigenen Version entspricht
(ADR-0088, löst ADR-0035, ADR-0068 und ADR-0086 ab). `GET /updates/{target}/{version}`
antwortet im Tauri-Updater-Format mit einer URL direkt auf das GitHub-Release
`https://github.com/Jeansy91/priva-sync/releases/download/v<Server-Version>/<Asset>`. Asset-Name
und Signatur je Plattform stammen aus `apps/server/internal/clientrelease/manifest.json`, das die
CI vor dem Image-Build schreibt und das per `go:embed` ins Binary wandert. Im Repository liegt
eine leere Variante; ein lokal gebauter Server bietet dadurch keine Updates an.

Die Antwort ist 204 No Content, wenn der Client schon auf der Server-Version ist, neuer als der
Server ist (kein Downgrade), das Target im Manifest fehlt oder das Manifest nicht zur
Server-Version passt. Clients aktualisieren sich beim Start selbst (ADR-0060).

Es gibt keine Release-Verwaltung im Server mehr (kein Upload, kein Import, keine Config).

**Voraussetzung: das GitHub-Repository muss öffentlich sein.** Die Release-Downloads laufen
anonym. Bei einem privaten Repository schlagen sie mit 404 fehl, automatische Updates
funktionieren dann nicht.

## Security-Gate

Der Security-Audit ist Release-Gate (ADR-0007, ADR-0030, ADR-0031). Lokal lässt er sich mit
`make security-audit` ausführen.
