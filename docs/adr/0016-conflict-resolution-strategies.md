# ADR-0016: Konflikt-Auflösung mit Strategien (keep_current / keep_conflict / keep_both)

**Status:** Accepted
**Datum:** 2026-06-15

## Kontext

[ADR-0003](0003-conflict-loser-copy.md) behält bei einem echten Konflikt beide
Versionen: die Loser-Bytes landen unter `<orig>.conflict-<ts>.<ext>` plus
`conflicts`-Row, der Sieger bleibt am Originalpfad. Die Auflösung war jedoch
einseitig — „auflösen" verwarf still die Loser-Kopie (faktisch immer
keep_current). Der User konnte die Konfliktversion nicht in der App
übernehmen, ohne manuell Dateien zu verschieben, und sah vor der Entscheidung
keinen Vergleich.

Designprinzip aus ADR-0003 gilt weiter: **kein stiller Datenverlust.** Die
Wahl, welche Bytes gewinnen, gehört dem User — explizit, nicht implizit.

## Entscheidung

Auflösen nimmt eine **Strategie**:

- **keep_current** — Originaldatei behalten, Loser-Kopie verwerfen (bisheriges
  Default-Verhalten; leerer Body bleibt dabei für Alt-Clients gültig).
- **keep_conflict** — Loser-Bytes ersetzen das Original (Versionssprung), die
  bisherigen Sieger-Bytes werden aus der Quota freigegeben.
- **keep_both** — Original bleibt, die Loser-Kopie wird zur regulären Datei am
  Konfliktpfad (nichts wird verworfen).

Eine unbekannte Strategie wird mit **400** abgelehnt; der Konflikt bleibt offen.

Gilt symmetrisch für **eigene Roots** und **Shared Mounts** (ADR-0011); beim
Mount entscheidet weiter `can_write`, und der Server löst unter dem Geber auf,
damit Storage/Quota beim Eigentümer bleiben (ADR-0010).

**Vergleich vor der Entscheidung:** `GET /conflicts/{id}` (bzw. die
Mount-Variante) liefert Metadaten beider Versionen; `…/file` serviert die
Loser-Bytes direkt aus dem Storage — nötig, weil Upload-/Move-Loser keine
`files`-Row am Konfliktpfad haben und der pfadbasierte Download sie nicht
findet. Die Web-UI zeigt darüber optional einen Zeilen-Diff (LCS) für
Textdateien.

**Desktop** bleibt lokal (ADR-0006-Engine): ein Konflikt ist kein Server-Row,
sondern ein lokaler Marker plus `.conflict.<ts>`-Kopie auf der Platte. Die drei
Strategien sind dort **lokale Datei-Operationen** (Kopie löschen / über das
Original legen + hochladen / als eigene Datei behalten). Eine Migration
speichert den Pfad der Kopie je Konflikt-Zeile, damit der Resolve sie findet;
der Diff vergleicht die beiden Dateien auf der Platte.

## Konsequenzen

**Positiv:**
- Vollständige Auflösung in der App, in beiden Clients, ohne manuelles
  Datei-Schieben.
- Der User entscheidet informiert (Metadaten + Text-Diff).
- Strategie-Vokabular ist über Server, Web und Desktop identisch
  (`keep_current` / `keep_conflict` / `keep_both`).

**Negativ:**
- Zwei Auflösungs-Pfade (Server-Row vs. lokale Datei-Ops) müssen getrennt
  gepflegt werden — die Desktop-Variante kann den Server-Resolve nicht
  wiederverwenden.
- Konflikt-Zeilen aus der Zeit vor der Desktop-Migration tragen keinen
  Kopie-Pfad und lassen sich nur per keep_current verwerfen.
- `keep_both` kann eine knappe Quota weiter belasten (beide Versionen bleiben),
  wie schon in ADR-0003 vermerkt.
