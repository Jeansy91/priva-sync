# ADR-0032: pnpm `trustPolicyExclude` fuer zwei bekannte False-Positives

- Status: Obsolet (2026-11-13): `tailwind-merge` ist mit Tailwind 4 auf v3
  gewechselt, `semver@6.3.1` wird nicht mehr installiert (nur noch 7.x);
  `trustPolicyExclude` ist entfernt. Tritt der False-Positive
  erneut auf, gilt das Vorgehen unten (exakte Version, kein Paketname).
- Datum: 2026-08-13
- Kontext: `trustPolicy: no-downgrade` (ADR-0031) blockiert zwei unbedenkliche
  Installs; Review bei jedem Dependency-Bump

## Kontext

ADR-0031 aktiviert `trustPolicy: no-downgrade` in `pnpm-workspace.yaml` als
Supply-Chain-Haertung. Fuer zwei Pakete schlaegt die Policy falsch an
(pnpm/pnpm-Diskussion #11084): `trustPolicy` vergleicht Publish-Evidence ueber
ALLE Major-Versionen eines Pakets hinweg, nicht nur innerhalb der aufgeloesten
Major-Version. `semver@6.3.1` und `tailwind-merge@2.6.1` haben beide parallele
Major-Branches, bei denen ein spaeterer Branch vor der hier installierten
Version Provenance-Attestation erhielt — das triggert
`ERR_PNPM_TRUST_DOWNGRADE`, obwohl weder Paket noch Version kompromittiert
ist (verifiziert gegen die Registry und oeffentliche Security-Scanner).

## Entscheidung

Beide Versionen exakt (nicht als Paketname-Wildcard) in
`pnpm-workspace.yaml` unter `trustPolicyExclude` ausgenommen. Ein spaeterer
echter Downgrade auf eine andere Version desselben Pakets faellt weiterhin
unter die Policy und schlaegt fehl.

Die Ausnahme ist an einen Review gebunden: bei jedem Dependency-Bump von
`semver` oder `tailwind-merge` pruefen, ob der pnpm-Upstream-Bug behoben ist.
Wenn ja, Eintrag entfernen; wenn nein, auf die neue Version umziehen. Kein
Dauer-Exclude ohne Review-Anlass — analog zur Inline-Suppression-Konvention
aus ADR-0031 (Begruendung an der Fundstelle, kein globales Stummschalten).

## Konsequenzen

- `pnpm install` installiert beide Pakete ohne manuelles `--no-verify-store`
  oder Abschalten von `trustPolicy` insgesamt.
- Die Ausnahme ist granular (exakte Version) und dokumentiert nachvollziehbar,
  ohne die Policy fuer alle anderen Pakete zu schwaechen.
- Erfordert manuelle Pflege bei Versions-Bumps; ohne diesen
  Schritt haeuft sich toter Exclude-Eintrag an.

## Verworfen

- **`trustPolicy` global deaktivieren** — verliert das Signal fuer echte
  Trust-Downgrades bei allen anderen Paketen.
- **Paketname ohne Versions-Pin ausschliessen** — wuerde auch einen
  zukuenftigen echten Downgrade auf diese Pakete stillschweigend durchlassen.
