# ADR-0055: Job-Outputs nur als Ja/Nein, Werte aus der Quelle

**Status:** Accepted
**Datum:** 2026-08-27

## Kontext

[ADR-0054](0054-ein-lauf-pro-release.md) lässt die Build-Jobs die Version aus
dem Output des Jobs beziehen, der den Tag geprägt hat, und schaltet sie über
`needs.tag-release.outputs.tagged == 'true'`.

Lauf 496 zeigt beim Auswerten dieser Outputs:

```
expression 'format('{0}', steps.tag.outputs.TAGGED)' evaluated to '%!t(string=false)'
expression 'format('{0}', steps.v.outputs.VERSION)' evaluated to '%!t(string=0.7.3)'
```

`%!t(…)` ist Go's Meldung für ein `%t`-Verb auf einem String. Von aussen sind
zwei Lesarten möglich und **nicht unterscheidbar**: entweder ist der
weitergereichte Wert selbst verstümmelt, oder nur die Log-Zeile formatiert
falsch und der Wert ist intakt.

Der Unterschied ist nicht akademisch. Unter der ersten Lesart wäre `tagged`
auch im Release-Fall `%!t(string=true)`, der Vergleich `== 'true'` also falsch
und die Build-Jobs unerreichbar — dieselbe Sorte toter Zweig, die ADR-0054
gerade beseitigt hat. Und `VERSION` würde als `%!t(string=0.7.3)` in ein
Image-Tag wandern.

## Entscheidung

**Kein Wert reist durch einen Job-Output.** Die Build-Jobs lesen die Version
selbst aus `apps/desktop/package.json` — die Datei liegt nach ihrem Checkout
ohnehin vor, und `scripts/bump-version.sh` schreibt alle Manifeste aus einem
Wert, das Manifest ist also die Quelle. Ein Wert, der eine Verstümmelung
unbemerkt überlebt, hätte als Image-Tag echten Schaden angerichtet.

**Das Ja/Nein reist weiter, aber verstümmelungstolerant geprüft:**
`if: contains(needs.tag-release.outputs.tagged, 'true')`. `contains` liefert
unter beiden Lesarten dasselbe richtige Ergebnis — `%!t(string=false)` enthält
kein `true`, `%!t(string=true)` schon, und die unverstümmelten `true`/`false`
ebenfalls. Damit muss die offene Frage nicht beantwortet werden, um korrekt zu
sein.

Verworfen: erst herausfinden, welche Lesart stimmt. Das kostet mindestens einen
weiteren Release-Zyklus, und die Antwort kann sich mit dem nächsten
Runner-Update wieder ändern. Eine Formulierung, die von der Antwort nicht
abhängt, ist billiger und bleibt richtig.

## Konsequenzen

**Positiv**

- Korrekt, unabhängig davon, ob der Runner Werte oder nur Logzeilen falsch
  formatiert — und bleibt es, wenn der Fehler behoben wird.
- Die Version steht an genau einer Stelle; kein Weg, auf dem sie sich
  unterwegs verändern kann.

**Negativ**

- Die Leseregel für die Version steht jetzt in drei Build-Jobs statt in einem
  Output. Ändert sich das Manifest-Format, sind es drei Stellen — dieselbe
  Duplikation, die die `needs`-Listen schon tragen, weil die Workflow-Syntax
  keine Anker kennt.
- `contains` statt `==` ist schwächer: ein Output `nottrue` würde ebenfalls
  greifen. Hier wird das Feld nur an einer Stelle geschrieben, mit genau zwei
  möglichen Werten.

## Referenzen

- ADR-0054 — dessen Versions-über-Output-Mechanismus wird hier ersetzt
- Lauf 496 — die Logzeilen oben
