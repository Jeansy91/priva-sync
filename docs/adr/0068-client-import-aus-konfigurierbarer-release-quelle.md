# ADR-0068: Client-Import aus konfigurierbarer Release-Quelle

**Status:** Abgelöst durch ADR-0088
**Datum:** 2026-09-05

## Kontext

Ein frisch aus dem öffentlichen Docker-Image gestarteter Server hatte ein leeres
`releasesPath` (ADR-0035): kein Client-Installer war verfügbar, bis ihn wer
manuell über `/admin/releases` hochlud. Die CI legte die Desktop-Bundles nur als
Workflow-Artefakte ab (begrenzte Aufbewahrung, an den CI-Lauf gebunden); ein
Publish-Job gegen die Admin-API scheiterte daran, dass deren Access-Token nach
15 Minuten abläuft und als statisches CI-Secret unbrauchbar ist.

## Entscheidung

Der Server importiert Client-Releases per Klick aus einer konfigurierbaren
Release-Quelle (`ReleaseSourceConfig` mit `url` und `token`, geschnitten wie
`DocumentServerConfig`, ADR-0027; kein eingebauter Default, leer heißt Feature
aus). Der Import nutzt den quellen-agnostischen Kern von `UploadRelease`
(Downgrade-Schutz, atomares Schreiben, Manifest-Merge); Binary und `.sig` werden
als eine Einheit importiert. Voraussetzung war, dass die CI echte Releases mit
Assets veröffentlicht, deren Namen das Target explizit tragen
(z. B. `privasync-linux-x86_64.AppImage` plus `.sig`).

## Konsequenzen

- Server macht ausgehende HTTP-Requests zu einem fest konfigurierten Host;
  URL und Token sind Deploy-Zeit-Config, keine Eingabe pro Request (kein
  SSRF-Vektor über eine Admin-Web-Session).
- Die Integrität sichert die minisign-Prüfung beim Client, nicht der
  Transportweg.

## Abgelöst

Der Server importiert, speichert und verteilt keine Client-Releases mehr; die
Update-Antwort zeigt direkt auf die GitHub-Release-Assets
(siehe [ADR-0088](0088-client-updates-direkt-von-github-passend-zur-server-version.md)).

## Referenzen

- ADR-0027 — `url`+Secret-Config ohne eingebauten Fallback
- ADR-0035 — Admin-Release-Verwaltung
- ADR-0053/0054 — CI-Release-Flow
