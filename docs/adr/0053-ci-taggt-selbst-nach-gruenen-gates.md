# ADR-0053: CI setzt den Tag, der Tag baut nur noch

**Status:** Superseded by ADR-0054
**Datum:** 2026-08-27

## Kontext

[ADR-0044](0044-single-ci-workflow.md) hat drei Workflow-Dateien zu einer
zusammengelegt, weil ein Release vier gleichzeitige Läufe auf einem einzelnen
Runner erzeugte. Aus vier wurden zwei — und zwei sind immer noch
zwei.

`make release` pushte Branch und Tag in einem einzigen `git push`, in der
ausdrücklichen Annahme, das ergebe einen Lauf. Die Annahme ist falsch: die CI
erzeugt pro Ref einen Lauf, und `concurrency.group: ci-${{ github.ref }}`
gruppiert nach Ref — `refs/heads/main` und `refs/tags/v0.7.2` sind
verschiedene Gruppen, also serialisiert nichts. Beide Läufe fahren denselben
Commit durch dieselben Gates, gleichzeitig, auf einem Runner.

Zweimal in Folge hat der Tag-Lauf das verloren, jeweils an geteiltem
Runner-Zustand und mit unterschiedlichem Symptom:

- **v0.7.2:** `actions/checkout@v4` bricht ab mit
  `remove /home/rootless/.cache/act/<hash>/.gitignore: no such file or
  directory`, danach `Cannot find module .../dist/index.js`. Der Runner
  hält seinen Action-Cache prozessübergreifend an einer Stelle; ein Lauf räumt
  ihn ab, während der andere daraus lädt.
- **v0.7.1:** ein Outbox-Test fällt mit `entry not found` um. Ursache
  unbewiesen — der Test ist inzwischen instrumentiert, damit der nächste
  Fehlschlag sie nennt.

Beide Male lief der Branch-Lauf auf demselben Commit grün durch, beide Male
wurden `server-image`, `desktop-linux` und `desktop-windows` übersprungen. Das
Ergebnis ist die unangenehmste Sorte Fehlschlag: ein Tag existiert, ein Release
scheint zu existieren, und es gibt keine Artefakte.

## Entscheidung

**Ein Release sind zwei Läufe, aber nie zwei gleichzeitig.**

**`make release` pusht nur den Branch.** Kein `git tag`, kein Tag im Push. Der
Aufruf endet mit dem Hinweis, dass CI den Tag setzt.

**CI taggt selbst, wenn alle Gates grün sind.** Der Job `tag-release` hängt per
`needs` an allen acht Gate-Jobs, läuft nur auf `main`, liest die Version aus
`apps/desktop/package.json` und legt `v<version>` über die GitHub-API an. Die
Version kommt aus dem Manifest, nicht aus der Commit-Message: `bump-version.sh`
schreibt alle Manifeste aus einem Wert, das Manifest ist also die Quelle, und
eine handgeschriebene Commit-Message kann nicht danebenliegen. Der Job ist
idempotent — ein vorhandener Tag wird in Ruhe gelassen, ein Nachlauf scheitert
also nicht an seiner eigenen vorherigen Arbeit.

Genutzt wird der pro Lauf injizierte Workflow-Token (`GITHUB_TOKEN`).

**Auf einem Tag laufen die Gates nicht mehr.** Jeder Gate-Job trägt
`if: "!startsWith(github.ref, 'refs/tags/')"`.
Die drei Build-Jobs verlieren ihr `needs` auf die Gates — die laufen in diesem
Lauf ja nicht — und hängen nur noch an ihrem Tag-`if`.

**Die Gate-Garantie wird durch Erreichbarkeit ersetzt, nicht aufgegeben.**
ADR-0044 ließ auf einem Tag alle Gates laufen, damit ein Tag-Push auf einem
ungeprüften Commit nichts baut. Stattdessen prüft jeder Build-Job jetzt
`git merge-base --is-ancestor "$GITHUB_SHA" origin/main` und bricht sonst ab.
Jeder Commit auf `main` fährt die Gates, also ist ein Tag, der in die
Geschichte von `main` zeigt, ein Tag auf einem geprüften Commit. Der Checkout
holt dafür die volle Historie.

Verworfen: `concurrency` auf den SHA statt auf den Ref umzustellen. Das
serialisiert zwar, aber `cancel-in-progress` wird für den *eingehenden* Lauf
ausgewertet — ein Branch-Lauf, der als zweiter einträfe, würde den Tag-Lauf
derselben Gruppe abbrechen. Genau das, was ADR-0044 als „broken release"
ausschließen wollte.

Verworfen: den Tag von Hand erst nach einem grünen Branch-Lauf zu pushen. Das
vermeidet die Gleichzeitigkeit auch, verlegt aber einen Wartepunkt in einen
Vorgang, den sonst ein einziger Befehl erledigt.

## Konsequenzen

**Positiv**

- Nie wieder zwei gleichzeitige Läufe desselben Commits; der geteilte
  Action-Cache des Runners bekommt keinen zweiten Zugreifer.
- Die Gates laufen pro Release genau einmal statt zweimal — das Ziel von
  ADR-0044, jetzt auch auf dem letzten Weg erreicht.
- Ein Tag entsteht nur noch für einen Commit, dessen Gates grün waren. Vorher
  konnte ein Tag jeden Commit bezeichnen und die Prüfung lief hinterher.
- Der Tag-Lauf ist kurz: kein Test, kein Scan, nur Build und Publish.

**Negativ**

- Der Tag erscheint erst einige Minuten nach dem Push, nicht sofort. Wer direkt
  nach `make release` nach `v<version>` sucht, findet ihn noch nicht.
- Ein handgesetzter Tag auf einen `main`-Commit, dessen Gate-Lauf rot war,
  käme durch den Erreichbarkeits-Guard. Schließbar über eine API-Abfrage nach
  einem erfolgreichen Lauf für den SHA; nicht gebaut, solange das nicht real
  vorkommt.
- Ein Tag auf einem Commit ausserhalb von `main` baut nicht mehr, sondern
  scheitert im Guard. Beabsichtigt, aber es ist ein Verhaltenswechsel.
- `tag-release` braucht Schreibrechte am Repository über den Task-Token. Fällt
  die weg, entsteht kein Tag und der Release bleibt stehen — sichtbar als roter
  Job, nicht als stiller Ausfall.

## Referenzen

- ADR-0044 — ein CI-Workflow; dessen Gate-auf-Tag-Regel wird hier ersetzt
- ADR-0039 — genau eine Serverinstanz
- Läufe 490/491 (v0.7.1) und 492/493 (v0.7.2) — die beiden Kollisionen
