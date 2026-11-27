# ADR-0086: GitHub-Releases als Release-Quelle

**Status:** Abgelöst durch ADR-0088
**Datum:** 2026-11-27

## Kontext

ADR-0068 führte den Client-Import aus einer konfigurierbaren Release-Quelle ein.
Die offiziellen Client-Releases liegen als GitHub-Releases im Repository
`Jeansy91/priva-sync`, öffentlich oder (bei privaten Repos) nur mit Token lesbar.
Bei privaten Repos taugt `assets[].browser_download_url` nicht für
authentifizierte Downloads; korrekt ist `GET assets[].url` mit
`Accept: application/octet-stream`, worauf GitHub mit einem Redirect auf einen
anderen Host (Release-Storage) antwortet.

## Entscheidung

1. **Nur GitHub als Quelle.** Eine Wire-Form, ein Codepfad; die Config
   (`release_source.url`/`token`) bleibt, `url` ist
   `https://api.github.com/repos/<owner>/<repo>`.
2. **Token optional.** Öffentliche Repos brauchen keines; für private Repos
   empfiehlt sich ein Fine-grained Personal Access Token mit `Contents: Read`
   auf genau dieses Repository. „Token ohne URL" bleibt ein Startfehler.
3. **Liste und Download:** `GET {url}/releases?per_page=100&page=N`; Drafts und
   Prereleases werden übersprungen. Der Download nutzt die API-URL des Assets;
   `FetchAsset` lehnt jede URL mit fremder Origin ab, damit das Token nur die
   API-Origin erreicht.
4. **Redirect-Allowlist.** Redirects werden nur auf dieselbe Origin oder auf
   feste `https`-Hosts der GitHub-Release-Storage gefolgt (exakter
   Host-Vergleich, maximal 10 Redirects); beim Origin-Wechsel wird der
   `Authorization`-Header entfernt. Die Liste ist bewusst Code, keine Config.

## Abgelöst

Der Server importiert und verteilt keine Client-Releases mehr; die
Update-Antwort zeigt direkt auf die GitHub-Release-Assets
(siehe [ADR-0088](0088-client-updates-direkt-von-github-passend-zur-server-version.md)).

## Referenzen

- ADR-0068 — Client-Import aus Release-Quelle
- ADR-0027 — `url`+Secret-Config ohne eingebauten Fallback
- ADR-0035 — Admin-Release-Verwaltung
