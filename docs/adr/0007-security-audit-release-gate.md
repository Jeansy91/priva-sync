# ADR-0007: Security-Audit als Release-Gate

**Status:** Accepted (SAST, Container-Scan, Secret-Scan, DAST aktiv — KI-Review geplant)
**Datum:** 2026-04-30

## Kontext

Self-Hosted-Tool mit Auth, Datei-Upload und öffentlichen Share-Links — die
Angriffsoberfläche ist nicht klein, aber das Projekt hat keine dedizierte
Security-Person. Ohne Release-Gate driftet Security-Hygiene zwischen
Releases.

CI prüft bei jedem Push (`gosec`, `cargo audit`, `npm audit`, `semgrep`,
`trivy`, `gitleaks`). Das fängt bekannte CVEs ab, aber kein DAST und keine
Domain-Logik-Lücken (Auth-Bypass, IDOR, Path Traversal in Anwendungs-
Pfaden).

## Entscheidung

`make security-audit` läuft vor jedem Release und besteht aus:

1. **SAST**: `gosec`, `cargo audit`, `npm audit`,
   `semgrep --config=p/owasp-top-ten`
2. **Container-Scan**: `trivy` über das Server-Image
3. **Secret-Scan**: `gitleaks` über Git-History
4. **DAST**: OWASP ZAP gegen lokale Test-Instanz
5. **KI-Review**: Claude liest `git diff {last-tag}...HEAD`, prüft auf
   Auth/Authz, Path Traversal, Injection, Krypto-Fehler, fehlende
   Input-Validierung, Datenlecks in Logs. Erzeugt
   `security-audit-report-{version}.md`.

**Kritische oder hohe Findings blockieren das Release.**

## Konsequenzen

**Positiv:**
- Reproduzierbares Gate, ein einziger Make-Target.
- KI-Review fängt Domain-Logik-Lücken, die statische Tools nicht sehen.
- Audit-Reports werden versioniert und liegen neben dem Release-Artefakt.

**Negativ:**
- Release dauert länger (ZAP-Scan + KI-Review brauchen Minuten).
- Falsch-Positive müssen manuell triagiert werden (gerade `semgrep` und
  KI).
- KI-Review ist abhängig vom verfügbaren Modell — Kosten + Qualität
  schwanken.
