# ADR-0034: Non-root Container-USER mit fester UID

- Status: Accepted
- Datum: 2026-08-21
- Kontext: letzter offener Punkt aus ADR-0031 ("Bewusst
  zurueckgestellt: Non-root-Container-USER")

## Kontext

ADR-0031 hat den Non-root-`USER` bewusst zurueckgestellt: der Server schreibt
ins bind-gemountete `/data`-Volume, das im Single-Node-Compose-Deployment
root-eigen ist. Ein `USER`-Wechsel ohne passende Volume-Ownership haette den
Container beim Start brechen lassen (`permission denied` auf `/data`).

## Entscheidung

Runtime-Stage im [Dockerfile](../../Dockerfile) legt Gruppe+User mit fester
numerischer ID an (`10001:10001`, nicht die naechste freie `adduser`-ID) und
wechselt per `USER privasync` vor dem `ENTRYPOINT`. Feste statt automatisch
vergebener ID, damit sie stabil ueber Image-Rebuilds bleibt und Operator-seitig
vorab referenzierbar ist.

Die Ownership-Verantwortung liegt beim Operator, nicht im Image:
[docker-compose.yml](../../docker-compose.yml) dokumentiert am Volume-Mount,
dass `./data` und `./config` vor dem ersten Start auf `10001:10001` gechownt
werden muessen. `/config` ist `:ro` gemountet — dort reicht Welt-Lesbarkeit,
kein Chown noetig, ausser das Host-Verzeichnis ist restriktiver als `644`.

## Konsequenzen

- `missing-user-entrypoint` (semgrep) ist ohne Suppression gruen — kein
  Rest-Punkt mehr aus ADR-0031.
- Bestehende Deployments muessen beim naechsten Image-Update einmalig
  `chown -R 10001:10001 ./data ./config` fahren, sonst startet der Server mit
  Schreibfehlern auf `/data`. Kein automatischer Migrationsschritt, da ein
  Chown auf einem fremden Host nicht blind vom Image ausgeloest werden soll.
- Neue Deployments (`mkdir` + `chown` vor `docker compose up`) sind ab Tag 1
  non-root.

## Verworfen

- **Naechste freie UID via `adduser` ohne `-u`** — verschiebt sich bei
  Base-Image-Aenderungen, macht den Operator-Chown-Befehl instabil.
- **Chown im Entrypoint zur Laufzeit (dann `su-exec` zu non-root)** — braeuchte
  den Container wieder root-startend, genau das Problem, das der `USER`-Wechsel
  loesen soll.
- **Named Volume statt Bind-Mount**, um Ownership Docker zu ueberlassen —
  groesserer Deployment-Umbau (Backup/Restore-Workflow haengt am Bind-Mount,
  SPEC.md), nicht durch ADR-0031 gedeckt.
