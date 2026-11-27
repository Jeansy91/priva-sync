# Admin-Release-Verwaltung: server-seitiger Manifest-Bau, inkrementelles Upload-Modell

Status: Abgelöst durch ADR-0088 (ursprünglich umgesetzt)

Neue Client-Releases werden künftig über eine Admin-Seite im Web hochgeladen statt
per `scripts/publish-release.py` + manuell exportiertem Admin-Token. Die
`manifest.json` (Tauri-Updater-Format) wird dabei **serverseitig** aus einzeln
hochgeladenen Plattform-Artefakten zusammengebaut, nicht im Browser repliziert —
das Wire-Format bleibt damit an der einen Stelle (Go) definiert, die es auch für
den öffentlichen Update-Feed (`GET /updates/{target}/{version}`) konsumiert.

Uploads sind **pro Plattform inkrementell**: eine Version wird angelegt und
Plattformen einzeln nachgereicht (Windows/Linux kommen automatisiert aus der
CI, macOS mangels nativem Runner weiterhin manuell). Jeder Upload
leitet das Manifest aus dem bereits vorhandenen Zustand plus dem neuen Artefakt
neu ab — Notes/Pub-Date werden dabei aus dem bestehenden `manifest.json`
übernommen statt erneut abgefragt zu werden. Der Server lehnt eine neue Version
ab, die nicht strikt höher ist als die bislang höchste bekannte (Schutz vor
versehentlichem Downgrade-Publish). Die Admin-Seite erlaubt volles CRUD: Notes
nachträglich editieren, einzelne Plattform-Artefakte ersetzen/löschen, ganze
Releases löschen.

## Considered Options

- **Browser baut Manifest** (Alternative): weniger neuer Server-Code, aber das
  Tauri-Updater-Format wäre doppelt gepflegt (Go liest es, TS schreibt es) —
  verworfen, weil das Wire-Format Teil der Update-Feed-Domäne ist, nicht der
  Web-UI.
- **Nur komplette Ein-Schritt-Uploads** (Alternative): einfacherer atomarer
  Zustand pro Version, aber erzwingt gleichzeitige Verfügbarkeit aller
  Plattform-Artefakte — passt nicht zur realen Build-Kadenz (macOS-Build läuft
  zeitversetzt und manuell).

## Consequences

- `scripts/publish-release.py` ist entfallen — der CI-Workflow-Kommentar in
  `.github/workflows/release.yml` verweist jetzt auf `/admin/releases` statt auf
  das Skript.
- Der bestehende Upload-Handler (`apps/server/internal/api/handler/update.go`)
  bekommt zusätzlich tmp+rename-Atomicität beim Dateischreiben, angeglichen an
  `storage.Service.WriteFile` / `avatar.go`.
- Keine Cross-Referenzierung mit Client-Fleet-Daten (`AdminClientsPage`,
  `client_version`) in dieser ersten Ausbaustufe — Releases- und
  Clients-Verwaltung bleiben getrennte Seiten.
