# ADR-0088: Client-Updates direkt von GitHub, immer passend zur Server-Version

**Status:** Accepted
**Datum:** 2026-11-27
**Löst ab:** [ADR-0035](0035-admin-release-management.md), [ADR-0068](0068-client-import-aus-konfigurierbarer-release-quelle.md), [ADR-0086](0086-github-releases-als-release-quelle.md)

## Kontext

Der Server verteilte Desktop-Clients bisher selbst: Admins luden Artefakte über
`/admin/releases` hoch (ADR-0035) oder importierten sie per Klick aus einer
konfigurierbaren Release-Quelle (ADR-0068, ADR-0086). Der Server baute daraus ein
Tauri-Manifest und lieferte die Binaries über `/updates/artifacts/...` aus.

Das hatte drei Nachteile. Erstens konnte der Server beliebige, auch nicht zu ihm passende
Client-Versionen anbieten — dabei ist der Client die Hälfte eines Sync-Protokolls, dessen
andere Hälfte der Server ist. Zweitens musste jede Instanz ihre Releases pflegen
(Upload, Import, Löschen), samt eigenem Speicher, Config-Block und Admin-Oberfläche.
Drittens lag die Quelle der Wahrheit doppelt vor: in GitHub und noch einmal auf der Platte
des Servers.

Mit dem Umzug nach GitHub (`Jeansy91/priva-sync`) liegen die offiziellen Client-Builds
ohnehin als Release-Assets dort.

## Entscheidung

**Der Server bietet Clients ausschließlich die Version an, die seiner eigenen entspricht.**
Eine Server-Version `x.y.z` gehört zum Release `vx.y.z`; Server und Client werden
gemeinsam versioniert und im selben CI-Lauf gebaut (ADR-0054).

**Die Update-Antwort zeigt direkt auf GitHub.** `GET /updates/{target}/{current_version}`
antwortet im Tauri-v2-Format (`version`, `pub_date`, `url`, `signature`) mit
`https://github.com/Jeansy91/priva-sync/releases/download/v<Server-Version>/<Asset>`.
Der Server liefert keine Binaries mehr aus. Die Repo-Basis-URL ist eine Konstante im Code
(`internal/clientrelease`), nicht konfigurierbar.

**Asset-Name und Signatur kommen aus einem Manifest, das zur Build-Zeit eingebettet wird.**
Die CI schreibt es nach dem Veröffentlichen der Client-Assets und vor `docker build`
(`scripts/build-client-manifest.sh`) nach
`apps/server/internal/clientrelease/manifest.json`; der Server bettet die Datei per
`go:embed` ein:

```json
{
  "version": "1.2.3",
  "pub_date": "2026-11-27T12:00:00Z",
  "platforms": {
    "windows-x86_64": { "asset": "privasync-windows-x86_64-setup.exe", "signature": "<Inhalt der .sig-Datei>" },
    "linux-x86_64":   { "asset": "privasync-linux-x86_64.AppImage",     "signature": "..." },
    "darwin-aarch64": { "asset": "privasync-darwin-aarch64.app.tar.gz", "signature": "..." },
    "darwin-x86_64":  { "asset": "privasync-darwin-x86_64.app.tar.gz",  "signature": "..." }
  }
}
```

Im Repository liegt eine leere Variante (`"version": ""`, keine Plattformen). `go build`
und Tests laufen damit ohne CI; ein lokaler oder per PR gebauter Server (Smoke-Build in
`sec-trivy`) bietet schlicht keine Updates an.

**Antwortregeln.** 200 nur, wenn das Manifest für genau die Server-Version gebaut wurde, einen
Eintrag für das Target hat und `current_version` strikt kleiner als die Server-Version ist.
In allen anderen Fällen 204 No Content, was der Tauri-Updater als „kein Update" liest:
Client gleich alt (aktuell), Client neuer als der Server (kein Downgrade, wie bisher: nur
strikt höhere Versionen werden angeboten, ADR-0060 bleibt unberührt), kein Eintrag für das
Target, leeres Manifest oder Manifest einer anderen Version als der des laufenden
Servers (Dev-Build). Ein unbekanntes Target bleibt 400.

**Entfernt** werden die Admin-Release-Verwaltung (Liste, Upload, Notizen, Löschen,
Import), `internal/releasesource`, `release_source.*` und `storage.releases_path` in der
Config, die zugehörigen Routen, Fehlercodes, Audit-Events, Web-Seiten, i18n-Keys und
API-Client-Funktionen sowie `/updates/artifacts/{version}/{file}`.

**CI-Reihenfolge** (Details in `docs/releasing.md`): Client-Builds (`desktop-linux`,
`desktop-windows`, `desktop-macos`) → `publish-client-release` (GitHub-Release mit
benannten Assets) → `server-image` (Manifest aus den `.sig`-Dateien, dann Image-Build und
Push). Das Server-Image entsteht damit zuletzt, weil seine Signaturen aus den fertigen
Clients stammen.

**macOS** wird jetzt mitgebaut: je ein Job pro Architektur (`aarch64-apple-darwin`,
`x86_64-apple-darwin`) statt eines Universal-Binaries, weil der Updater die Plattformschlüssel
`darwin-aarch64` und `darwin-x86_64` getrennt nachschlägt. Ergebnis sind `.dmg` (Erstinstallation)
und `.app.tar.gz` mit `.sig` (Updater). Ohne Apple-Zertifikat, nur Ad-hoc-Signatur; Apple-Signierung
und Notarisierung lassen sich später im Job ergänzen.

## Konsequenzen

- **Das Repository muss öffentlich sein.** Der Updater lädt die Assets anonym von GitHub.
  Bei einem privaten Repository schlägt jeder Download mit 404 fehl; automatische Updates
  funktionieren dann nicht. Das gilt auch für den Download-Link in der Web-UI.
- Der Server ist für den Download kein Nadelöhr mehr, Selbst-Hoster brauchen keine
  Release-Pflege und keinen Token mehr. Dafür hängt die Update-Auslieferung an der
  Erreichbarkeit von GitHub (`github.com`, Redirect auf `release-assets.githubusercontent.com`).
- Die Server-Version und die Client-Version laufen im Gleichschritt. Ein Server-Image ohne
  eingebettetes Manifest (lokaler Build) bietet keine Updates an; ein Hotfix nur für den
  Client ist nicht mehr ohne neuen Server-Release möglich.
- Wer ein Release aus der Pipeline nachträglich korrigieren will, kann die Assets auf GitHub
  nicht einfach ersetzen: die Signaturen stecken im Image. Ein Fix ist ein neues Release.
- **Datenbank und Speicher.** Die Release-Verwaltung hatte keine Datenbanktabellen, sie lag
  ausschließlich als Dateien unter `{storage.data_path}/releases/` (bzw. dem früheren
  `storage.releases_path`). Eine Migration entfällt deshalb. Der Server löscht diese Dateien
  nicht selbst beim Start: der Pfad konnte frei konfiguriert sein, und stilles Löschen von
  Betreiberdaten durch ein Update ist die falsche Voreinstellung. Betreiber entfernen das
  Verzeichnis einmalig von Hand (`docs/deployment.md`). Bestehende Configs mit
  `release_source` oder `storage.releases_path` bleiben lauffähig, die Schlüssel werden
  ignoriert.
- Alte Audit-Log-Einträge `admin.release_*` bleiben in der Tabelle erhalten und werden als
  Rohtext angezeigt; es kommen keine neuen hinzu.
- Offen: macOS-Builds sind nicht notarisiert. Beim ersten Start greift Gatekeeper.
