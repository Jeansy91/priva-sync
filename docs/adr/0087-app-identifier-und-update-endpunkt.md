# ADR-0087: App-Identifier, Herausgeber und Update-Endpunkt aus der Server-URL

**Status:** Accepted
**Datum:** 2026-11-27

## Kontext

Tauri leitet aus dem App-Identifier alle App-Verzeichnisse ab (Roaming:
`privasync.db` samt WAL/SHM; Local: Logs, WebView2-Profil, `ExplorerNamespace`,
Upload-Snapshots; Linux/macOS entsprechend unter `~/.local/share`, `~/.config`,
`~/Library/...`) und, sofern nicht gesetzt, den Herausgeber des Installers. Der
Update-Endpunkt eines Clients muss zum Server passen, mit dem er arbeitet.

Keine Bindung an den Identifier haben: die Cloud-Files-Provider-ID
(`PrivaSync!<sid>!<account>`), die Explorer-Namespace-CLSID, das
Sparse-Package (`PrivaSync.Shell`), die Pipe und das Deep-Link-Schema
`privasync://`. Der Autostart-Eintrag und die Toast-AUMID folgen dem
Produktnamen bzw. dem Installationspfad, nicht dem Identifier.

## Entscheidung

**Identifier.** `io.github.jeansy91.privasync`. Einzige Quelle ist
`tauri.conf.json`; Code ohne `AppHandle` (Logging, Namespace-Pfade) liest ihn
über `app_id::identifier()`, das die Datei zur Compile-Zeit einbettet.
Bestehende Installationen wurden mit dem letzten Release vor dem Umzug auf
diesen Identifier umgestellt; dieser Stand enthält keinen Übernahmecode mehr.

**Installer (NSIS, Tauri 2.10.x, Bundler-Template geprüft).** Tauri leitet
`bundle.publisher` aus dem zweiten Teil des Identifiers ab; das ergäbe
„jeansy91“. `tauri.conf.json` setzt deshalb `bundle.publisher` explizit auf
`PrivaSync`. Im Template (`installer.nsi` des tauri-bundler) hängt Folgendes an
Herausgeber, Produktname oder Identifier:

- `UNINSTKEY` (`...\Uninstall\${PRODUCTNAME}`): nur Produktname.
- Installationsordner (`$LOCALAPPDATA\${PRODUCTNAME}` bzw. `$PROGRAMFILES64\...`),
  Startmenü-Verknüpfung, Autostart-Wert: nur Produktname.
- `MANUPRODUCTKEY` (`Software\${MANUFACTURER}\${PRODUCTNAME}`, Herausgeber plus
  Produktname): enthält den Installationsordner, den das Template beim Ersetzen
  der alten Version und in `RestorePreviousInstallLocation` liest. Der
  Herausgeber ist fest „PrivaSync“, der Schlüssel damit stabil.
- `BUNDLEID` (Identifier): nur beim Deinstallieren mit „App-Daten löschen“ für
  `%APPDATA%` und `%LOCALAPPDATA%`; der WebView2-Datenordner liegt unter dem
  Identifier und gehört nicht zum Installer.
- MSI: der Upgrade-Code kommt aus dem Produktnamen.

`installer-hooks.nsh` enthält nur die Hooks für die Explorer-Kontextmenü-
Erweiterung (ADR-0080).

**macOS.** `tauri.conf.json` enthält `bundle.macOS` mit
`minimumSystemVersion` 11.0 (Apple Silicon braucht mindestens 11), ohne
Apple-Zertifikat: `signingIdentity` `-` (Ad-hoc-Signatur) statt `null`, weil
arm64-Binaries ohne Signatur nicht starten; `hardenedRuntime` aus, da Ad-hoc ohne
Notarisierung davon nichts hat. Gatekeeper warnt beim ersten Start
(Rechtsklick, Öffnen). Updater-Artefakte (`.app.tar.gz` plus Signatur) sind von
Apple unabhängig und mit dem Minisign-Schlüssel signiert. Die Windows-Ressourcen
(Shell-DLL, MSIX, Skripte) stehen in `tauri.windows.conf.json` und fehlen auf
macOS; `build-shell.mjs` und `build-sparse-package.mjs` überspringen dort.

**Updater.** `tauri.conf.json` enthält keinen Endpunkt (der Public Key
steht dort). Der Client leitet ihn zur Laufzeit aus der Server-URL ab
(`<server_url>/updates/{{target}}-{{arch}}/{{current_version}}`, über
`updater_builder().endpoints(..)`): aus der Eingabe auf dem Login-Bildschirm,
sonst aus `server_url` der LocalDb. Ohne bekannten Server gibt es keine
Update-Prüfung.

## Verworfen

- **Fester GitHub-Endpunkt für Updates:** Der Server liefert die Updates
  selbst, und der Client kennt ihn aus der Anmeldung; ein fester Endpunkt wäre
  bei selbst gehosteten Instanzen falsch.
