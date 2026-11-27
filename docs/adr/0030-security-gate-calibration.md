# ADR-0030: Security-CI-Gates kalibrieren statt Rauschen unterdruecken

- Status: Accepted
- Datum: 2026-08-08
- Kontext: CI-Gates und Findings-Review (ADR-0031)

## Kontext

Die `security.yml`-Gates (gosec, cargo-audit, pnpm-audit, semgrep, trivy,
gitleaks) waren seit ihrer Einfuehrung durchgehend rot und liefen faktisch nie
durch (analog zum Coverage-Gate, siehe Infra-Fixes `dc1f974`/`0d6bf72`/
`b3e6ad2`). Bei der ersten realen Messung zeigte sich ein Mix aus echten,
fixbaren Befunden und bekanntem Scanner-Rauschen. Blindes `|| true` oder
Blanket-Disable haette echte Sicherheitsbefunde versteckt.

## Entscheidung

Jedes Gate wird **kalibriert**, nicht unterdrueckt: echte Befunde werden gefixt
oder als offener Befund getrackt; nur nachweislich falsch-positive bzw. reine
Konventions-Regeln werden mit dokumentierter Begruendung ausgeschlossen.

### cargo-audit — fixen, nicht ignorieren

Die 8 fehlerausloesenden Advisories sind fixbare Upgrades, kein
unfixbares Transitive-Set. Per `cargo update` gehoben:

- `crossbeam-epoch` 0.9.18 → 0.9.20 (RUSTSEC-2026-0204)
- `rustls-webpki` 0.103.11 → 0.103.13 (RUSTSEC-2026-0098/0099/0104 —
  Zertifikats-Namenspruefung + Panic beim CRL-Parsen; TLS-relevant fuer den
  Sync-Client)

`quick-xml` (RUSTSEC-2026-0194/0195, DoS) ist transitiv von Tauri gepinnt
(`plist ^0.38`, `tauri-winrt-notification ^0.37`); 0.41 ist nicht
semver-erreichbar, bis Tauri nachzieht. Deshalb per `--ignore` mit Verweis auf
diese ADR ausgeschlossen. Die 23 `unmaintained`/`unsound`-Warnungen (gtk-rs GTK3,
anyhow, glib, rand, …) sind non-failing und bleiben sichtbar.

### pnpm-audit — Prod-Highs fixen, Gate auf --prod scopen

Die ausgelieferten (Prod-)High-Advisories sind gehoben:

- `react-router` 7.14.0 → 7.18.2 (DoS via Route-Matching, RSC-CSRF-Bypass)
- `@xmldom/xmldom` 0.7.13 → 0.8.13 via pnpm-Override (5× XML-Injection); das
  verwundbare 0.7.13 wird transitiv von `epubjs` gezogen.

Das Gate laeuft jetzt mit `--prod --audit-level=high`: die verbleibenden
High-Advisories liegen ausschliesslich in Dev-Tooling (vitest→vite→postcss→
nanoid, esbuild), das nie ins deployte Artefakt gelangt. Prod-Scope ist damit
sauber (0 High); Dev-Tooling-Upgrades sind als offener Punkt getrackt. Verifiziert:
`pnpm audit --prod --audit-level=high` Exit 0; Web 631/631, Desktop 132/132,
Web-Build gruen.

### gosec — Rausch-/Konventionsregeln aus, Taint-Regeln an

Ausgeschlossen: `G104` (Best-Effort-`Close`/`Remove`), `G301/G302/G306/G307`
(Datei-/Ordner-Permissions-Konventionen), `G204`/`G304` (heuristische exec-/
Pfad-Regeln, abgeloest durch die praeziseren taint-basierten `G702`/`G703`,
die aktiv bleiben). Die verbleibenden echten Befunde (`G703` Path-Traversal,
`G702` Arg-Injection, `G124` Cookie-Secure, `G120` Form-Limit, `G118` Context,
`G101` LOW) werden im Findings-Review (ADR-0031) reviewt — die Ausschlussliste wird **nicht**
erweitert, um sie stillzulegen.

### semgrep — supply-chain-Pinning-Regel aus, App-Regeln an

`github-actions-mutable-action-tag` (~60 Treffer, jeder `uses: …@vX`) verlangt
40-stellige SHA-Pins — bei First-Party-Actions geringer
Wert; separat im Findings-Review (ADR-0031). Build-Scripts + generierter Code + Tests via
`.semgrepignore` ausgenommen. Echte App-Befunde (Cookie-Secure, XSS via
`dangerouslySetInnerHTML`, `filepath.Clean`-Misuse, Dockerfile-`USER`) bleiben
und werden im Findings-Review (ADR-0031) bearbeitet.

### trivy / gitleaks — Infra

`unrar` → `unar` (Alpine-Lizenz, siehe Commit `2946145`) macht den
Image-Build wieder lauffaehig; gitleaks-Modulpfad korrigiert (`b3e6ad2`).

## Konsequenzen

- cargo-audit, pnpm-audit, trivy(-Build), gitleaks, test-desktop-rust werden
  gruen; die Upgrades beheben echte CVEs (TLS, DoS, CSRF, XML-Injection).
- gosec und semgrep bleiben **bewusst rot**, bis die im Findings-Review (ADR-0031) behandelten
  echten Befunde reviewt/gefixt sind. Ihr Rot ist jetzt aussagekraeftig (nur echte
  Befunde), nicht von Rauschen ueberdeckt.
- Die Ausschlusslisten sind an dieser ADR verankert; Erweiterungen erfordern
  eine bewusste Entscheidung, kein stilles Stummschalten.

## Verworfen

- **`|| true` / Blanket-Disable** — versteckt echte Befunde, verletzt die
  Security-Prioritaet.
- **Alle 160 Findings blind unterdruecken** — dito, plus Verlust der
  Taint-Signale.
- **quick-xml/xmldom hart forken** — unverhaeltnismaessig gegenueber Override
  (xmldom) bzw. Upstream-Warten (quick-xml).
