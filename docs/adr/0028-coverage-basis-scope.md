# ADR-0028: Coverage-Basis fuer das AC-Ziel des Servers

**Status:** Accepted
**Datum:** 2026-08-07

## Kontext

Gefordert ist ein Coverage-Gate mit AC-Ziel **≥ 80 %** je Komponente.
Web, Desktop-TS und Rust erreichen ihre Ziele; der Go-Server liegt nach
Runde 7 bei 69.4 % (statement-gewichtet, `-coverpkg=./...`).

Eine statement-gewichtete Analyse des Rueckstands zeigt: der Gap sitzt
ganz ueberwiegend in echtem, testbarem HTTP-Handler-Code (`internal/api/
handler` bei 64.4 %, ~900 unabgedeckte Statements in normalen Handlern —
shares, upload, files_mutate, admin, auth_*, delta, trash). Zwei kleine
Bereiche druecken den Schnitt jedoch, ohne echten Testwert zu bieten:

- **`cmd/*`** (fuenf Entry-Points, 277 Statements, 0 %) — Prozess-Bootstrap
  ohne sinnvolle Unit-Oberflaeche; wird, wenn ueberhaupt, ueber End-to-End
  abgedeckt, nicht ueber `go test`.
- **`internal/api/handler/thumbnail_3d.go`** (3D-Mesh, STL/OBJ) und
  **`thumbnail_extra.go`** (HEIC/Comic) — 187 Statements, ~14 %. Reine
  Dekodierung von Fremdformaten; ein Test misst hier fast nur fremde
  Decoder-Libs und braucht binaere Fixtures im Repo.

`thumbnail.go` ist bewusst **nicht** betroffen: es ist gemischter Handler-
plus Hilfscode (`Thumbnail`/`serveThumbnail`/`encodeThumb`/`squareThumbnail`/
`matchesMagic`) und bleibt regulaeres Testziel — nur die reinen Decoder-Pfade
`extractVideoFrame`/`extractPDFFirstPage` brauchen externe Binaries.

## Entscheidung

1. **Die 80-%-Coverage-Basis des Servers ist der testbare Kern.** Aus der
   Messung ausgenommen sind `cmd/*`, `thumbnail_3d.go` und
   `thumbnail_extra.go`. Das AC-Ziel (≥ 80 %) gilt unveraendert, aber auf
   dieser Basis.

2. **Umsetzung per Profil-Filter, nicht per `-coverpkg`.** Der Ausschluss
   erfolgt in `scripts/check-coverage-go.sh`: das go-cover-Profil wird vor
   der Prozentberechnung um die ausgeschlossenen Bloecke bereinigt
   (`EXCLUDE_RE`). Grund: `-coverpkg` arbeitet nur auf Paket-Granularitaet
   und koennte einzelne Dateien (`thumbnail_*.go`) innerhalb von
   `internal/api/handler` nicht ausnehmen. Das volle Profil bleibt fuer
   `coverage.html` erhalten — gefiltert wird ausschliesslich die Gate-Zahl.

3. **Der Ausschluss ist eng und begruendet, kein Freibrief.** Aufgenommen
   wird nur Code, der entweder keine Unit-Oberflaeche hat (`cmd/*`) oder
   ueberwiegend Fremdformat-Dekodierung mit Binaer-Fixtures ist. Neue
   Eintraege in `EXCLUDE_RE` erfordern denselben Nachweis und eine
   Aktualisierung dieser ADR.

## Konsequenzen

- Die gemeldete Gate-Zahl steigt von 69.4 % auf **73.5 %** (Kern-Basis),
  ohne dass eine Zeile Test dazukam — sie misst jetzt das Richtige. Die
  CI-Schwelle wird entsprechend von 67 auf **71** angehoben
  (`COVERAGE_THRESHOLD_SERVER`, `.github/workflows/check.yml`,
  `.github/workflows/release.yml`).
- Der Weg zu 80 % bleibt reale Arbeit: ~466 zusaetzlich abgedeckte
  Statements, ganz ueberwiegend in normalen Handlern — kein Test-Theater,
  keine Fixtures. Das ist Folge-Backlog fuer weitere Coverage-Runden.
- `coverage.html` zeigt weiterhin auch die ausgeschlossenen Dateien, damit
  ihr tatsaechlicher Stand sichtbar bleibt und die Ausnahme ueberpruefbar
  ist.
