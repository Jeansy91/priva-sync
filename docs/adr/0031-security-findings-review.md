# ADR-0031: Security-Findings-Review — Inline-Suppression, echte Fixes, ein bewusstes Deferral

- Status: Accepted
- Datum: 2026-08-09
- Kontext: Findings-Review, baut auf ADR-0030 (Gate-Kalibrierung) auf

## Kontext

ADR-0030 hat die Security-Gates kalibriert und die verbleibenden High-Signal-
Findings (gosec G7xx/G124/G120/G118/G101/G115, semgrep Cookie/XSS/filepath/
Dockerfile/pnpm) zum Review geparkt. Dieser Review ist jetzt
durchgefuehrt. Jeder Befund wurde einzeln geprueft: echt → gefixt; false-positive
→ inline mit Begruendung unterdrueckt; ein Befund mit Deployment-Blast-Radius →
bewusst zurueckgestellt.

## Entscheidung

### False-Positives inline unterdruecken, nicht die Regel global abschalten

Verifizierte FPs werden per `//#nosec <rule> -- Grund` (gosec) bzw.
`// nosemgrep: <rule-id> -- Grund` (semgrep) **an der Fundstelle** annotiert. Die
globale Ausschlussliste aus ADR-0030 wird **nicht** erweitert — so bleibt jede
Regel fuer neue, echte Treffer scharf. gosec meldet damit 0 Issues bei 28
dokumentierten Nosec-Annotationen.

Betroffene FP-Klassen: G703 Path-Traversal (validierte `validPathSegment`-Eingaben
bzw. aus Content-Hash/UUID zusammengesetzte Cache-Pfade), G702 auf
server-generierte Temp-Pfade, G120 (Body bereits per `MaxBytesReader` gedeckelt),
G118 (Prozess-Lifetime-Mailer-Worker ohne Request-Kontext), G101 (Event-/
Error-Code-Konstanten), G115 (bounded/anzeige-only Integer-Konvertierungen),
semgrep XSS (`GitignoreEditor` escaped jeden User-Teil vor dem `innerHTML`).

### Echte Fixes

- **Cookie-Secure (G124 / cookie-missing-secure):** Neuer Helfer
  `AuthHandler.secureCookie(r)` = `r.TLS != nil || https-baseURL`. Hinter dem
  TLS-terminierenden Caddy-Proxy (SPEC.md) ist `r.TLS` nil, das alte
  `Secure: r.TLS != nil` lieferte dort **unsichere** Cookies. Jetzt erzwingt die
  https-`base_url` das Secure-Flag. Bleibt dynamisch (kein Literal `true`), damit
  Dev ueber HTTP weiter funktioniert — daher zusaetzlich `nosemgrep`/`nosec`.
- **Arg-Injection (G702, `bsdtar -xOf`):** `--`-Optionsterminator vor dem
  Archiv-Entry-Namen, damit ein Entry wie `-x` nicht als Flag interpretiert wird.
- **Static-Serving-Containment (filepath-clean-misuse):** Prefix-Check nutzt jetzt
  `h.root + PathSeparator` (verhindert Sibling-Bypass `<root>-evil`); `filepath.Clean`
  ist ausdruecklich nicht der Sanitizer, sondern die Abs-Root-Enthaltungspruefung.

### pnpm-Supply-Chain real aktiviert

`blockExoticSubdeps: true`, `minimumReleaseAge: 10080` (7 Tage), `trustPolicy:
no-downgrade` in `pnpm-workspace.yaml`. `--frozen-lockfile`-Installs sind von der
Release-Age-Quarantaene nicht betroffen (nur Resolution-Zeit). Aktiv
sicherheitsgepatchte Dev-Transitiven sind per `minimumReleaseAgeExclude`
ausgenommen — eine frische Security-Version eine Woche zurueckzuhalten waere der
falsche Trade-off.

### Dev-Tooling-Highs gepatcht

Per `pnpm.overrides` auf gepatchte Floors gehoben: `postcss ^8.5.18`,
`nanoid ^3.3.17`, `undici ^7.29.0`, `vite ^6.4.3` (Path-Traversal, DoS,
Cache-Disclosure). Nach dem Bump: Web 631/631, Desktop 132/132 gruen; voller
`pnpm audit --audit-level=high` = 0 High.

### trivy-Scan ohne Marketplace-Action

`aquasecurity/trivy-action@master` ist ungepinnt und zieht zur Laufzeit ein
Installer-Script nach. Stattdessen installiert der Job ein gepinntes trivy-Release
mit geprüfter Prüfsumme und scannt das lokal gebaute Image; die Vuln-DB kommt aus
dem AWS-ECR-Mirror, was die Rate-Limits von ghcr umgeht.

### Bewusst zurueckgestellt: Non-root-Container-USER

Der Server schreibt in das per Bind-Mount eingehaengte, im aktuellen Single-Node-
Caddy-Deployment **root-eigene** `/data`-Volume (SPEC.md). Ein non-root `USER`
braucht ein operator-seitiges `chown` von `./data`/`./config` auf die Container-UID
— ein koordinierter Deploy-Schritt, keine reine Code-Aenderung. Bis dahin per
`nosemgrep` dokumentiert.

## Konsequenzen

- gosec und semgrep werden gruen, ohne die High-Signal-Regeln global stumm zu
  schalten — ihr Signal bleibt fuer neue Treffer erhalten.
- Ein echter Prod-Cookie-Schwachpunkt hinter dem Reverse-Proxy ist geschlossen.
- Jede Suppression traegt eine Begruendung an der Fundstelle; Review ist im Diff
  nachvollziehbar.
- Ein Rest bleibt bewusst offen (Container-USER) — dokumentiert, nicht vergessen.

## Verworfen

- **Rule global excluden**, um FPs stumm zu stellen — verliert das Signal fuer
  echte kuenftige Treffer (ADR-0030-Grundsatz).
- **Cookie hart `Secure: true`** — braeche lokale HTTP-Dev-Logins.
- **USER blind setzen** — braeche Schreibzugriff auf das root-eigene Data-Volume
  im laufenden Deployment.
- **minimumReleaseAge senken/weglassen**, um Dev-Patches zu installieren — hebelt
  die Supply-Chain-Quarantaene fuer *alle* Pakete aus; der gezielte Exclude ist
  praeziser.
