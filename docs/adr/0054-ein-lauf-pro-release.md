# ADR-0054: Ein Lauf pro Release — Gates, Tag und Publish in derselben Ausführung

**Status:** Accepted — Versions-Weitergabe per Job-Output abgelöst durch ADR-0055
**Datum:** 2026-08-27

## Kontext

[ADR-0053](0053-ci-taggt-selbst-nach-gruenen-gates.md) hat den Release in zwei
Läufe geteilt: der Push auf `main` fährt die Gates und legt am Ende den Tag an,
der Tag löst einen zweiten Lauf aus, der nur baut und publiziert.

Der zweite Lauf kommt nie.

Ein Ref, das mit dem pro Lauf injizierten Workflow-Token angelegt wird, löst
**keine** weiteren Workflow-Läufe aus — Schleifenschutz der Plattform.
Beim ersten Durchlauf mit v0.7.3 war das direkt zu sehen: der Tag entstand auf
`700751d`, `tag-release` meldete Erfolg, und es folgte kein Lauf. Dass der
Trigger nicht das Problem ist, zeigt der Vergleich — dasselbe `tags: - 'v*'`
hatte bei v0.7.1 und v0.7.2 zuverlässig gefeuert, als der Tag von einem
Arbeitsplatz kam. Der einzige Unterschied ist der Urheber des Refs.

`server-image`, `desktop-linux` und `desktop-windows` waren damit tote Zweige:
in jedem Lauf sichtbar, in keinem erreichbar.

## Entscheidung

**Ein Release ist ein Lauf.** Der Push des Release-Commits auf `main` fährt die
Gates, legt den Tag an und baut und publiziert — alles in derselben Ausführung.

**`tag-release` gibt aus, ob es getaggt hat.** Der Job liefert `version` und
`tagged`; `tagged` ist nur wahr, wenn in diesem Lauf ein *neuer* Tag
geschrieben wurde. Die drei Build-Jobs hängen über
`needs: [tag-release]` daran und laufen unter
`if: needs.tag-release.outputs.tagged == 'true'`. Ein gewöhnlicher Commit auf
`main` trägt eine bereits getaggte Version, publiziert also nichts; erst der
Versionssprung in den Manifesten macht den Unterschied.

**Die Version kommt aus dem Job, der sie geprägt hat**, nicht mehr aus
`GITHUB_REF` — der Ref dieses Laufs ist `main`, nicht der Tag.

**Kein Tag-Trigger mehr.** Ein Tag ist jetzt ein Ergebnis, kein Auslöser. Ein
von Hand gesetzter Tag baut nichts; wer veröffentlichen will, hebt die Version
und pusht auf `main`.

**Der Erreichbarkeits-Guard aus ADR-0053 entfällt ersatzlos.** Er prüfte, ob
ein getaggter Commit von `main` erreichbar ist, weil ein Tag-Lauf sonst an den
Gates vorbeigebaut hätte. Diesen Weg gibt es nicht mehr: publiziert wird nur
noch aus einem `main`-Lauf, der alle Gates hinter sich hat. Die Garantie aus
ADR-0044 ist damit strenger als zuvor, nicht schwächer — und sie braucht keine
eigene Prüfung mehr, sie folgt aus der Struktur.

**`cancel-in-progress` ist jetzt immer `false`.** Solange ein `main`-Lauf nur
Tests fuhr, war Abbrechen gratis. Jetzt publiziert er, und ein Abbruch könnte
einen Release mitten im Image-Push zerreissen. ADR-0044 hat dieselbe Abwägung
schon einmal getroffen — „ein halb gepushtes Image ist schlimmer als ein
doppelter Lauf" —; nur der Fall, für den sie gilt, ist umgezogen.

Verworfen: einen Personal Access Token für `tag-release` zu benutzen, damit der
Tag doch einen Lauf auslöst. Das hielte die Zwei-Lauf-Struktur am Leben,
bräuchte ein weiteres Secret und liesse die Gleichzeitigkeit wieder zu, sobald
jemand zusätzlich von Hand taggt. Der Umweg löst ein Problem, das ohne ihn
nicht existiert.

## Konsequenzen

**Positiv**

- Ein Release erzeugt genau einen Lauf. Die Kollision aus ADR-0053 ist nicht
  mehr vermieden, sondern strukturell unmöglich.
- Die Gates laufen pro Release einmal.
- Publiziert wird ausschliesslich hinter grünen Gates, ohne dass eine eigene
  Prüfung das absichern muss.
- Der Tag entsteht weiterhin nur für einen geprüften Commit.

**Negativ**

- Ein überholter `main`-Lauf wird nicht mehr abgebrochen. Auf einem einzelnen
  Runner kostet das Zeit, wenn schnell hintereinander gepusht wird.
- Ein Release-Lauf ist länger als ein reiner Gate-Lauf, weil Build und Publish
  hinten dranhängen statt in einem eigenen Lauf zu stehen.
- Ein von Hand gesetzter Tag tut nichts mehr. Beabsichtigt, aber es ist ein
  Verhaltenswechsel gegenüber allem vor ADR-0053.
- Die Build-Jobs sind in jedem `main`-Lauf sichtbar und meistens übersprungen.
  Das liest sich in der Übersicht wie ein toter Zweig, ist aber der normale
  Zustand — sie werden genau dann wahr, wenn die Version steigt.

## Referenzen

- ADR-0053 — abgelöst; dessen Zwei-Lauf-Aufteilung setzt einen Lauf voraus,
  den der Schleifenschutz nie startet
- ADR-0044 — ein CI-Workflow; dessen Publish-hinter-Gates-Garantie gilt weiter
- Lauf 495 und der ausgebliebene Nachfolger — der Beleg
