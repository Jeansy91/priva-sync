# Entwicklung

## Voraussetzungen

- Go (Version siehe `apps/server/go.mod`), Node.js mit pnpm, Rust-Toolchain für den
  Desktop-Client, PostgreSQL für den Server.
- Abhängigkeiten installieren: `pnpm install`.
- `.env` aus `.env.example` anlegen. Pflicht für den lokalen Server sind `DB_HOST`,
  `DB_PASSWORD` und `JWT_SECRET`.
- Optional: `make install-hooks` richtet den pre-commit-Hook ein. Er regeneriert
  `events.gen.ts`, sobald Go-SSE-Structs geändert wurden (benötigt `tygo`).

## Befehle

```bash
make dev-server        # Go-Server, braucht .env
make dev-web           # Vite-Dev-Server
pnpm --filter @privasync/desktop tauri dev
make build             # Server + Web
make typecheck         # TypeScript-Prüfung aller Pakete
make lint-server       # go vet
make gen-events        # TS-Typen aus Go-SSE-Structs (tygo)
make help              # alle Targets
```

## Tests

```bash
make test-server                       # go test ./... -p 1
pnpm --filter @privasync/web test      # vitest
pnpm --filter @privasync/desktop test  # vitest
cd apps/desktop/src-tauri && cargo test
```

- Die Go-Tests teilen sich eine PostgreSQL-Datenbank. Deshalb laufen sie mit `-p 1`
  seriell; parallele Pakete würden sich gegenseitig stören (Zähler, Dateiumbenennungen,
  Outbox-Claims).
- CI misst Coverage für Server und Desktop mit Gate. Die Schwellwerte sind gemessene
  Baselines und werden nur angehoben, nie gesenkt (siehe [Testabdeckung](#testabdeckung), ADR-0028).
- Rote Tests werden vor dem Commit behoben, auch wenn sie bereits vorher rot waren.

### Tests ohne eigene Datenbank

Die Server-Tests brauchen nur `TEST_DATABASE_URL`. CI startet dafür pro Test-Job einen
frischen PostgreSQL-Service (`test-go` in `.github/workflows/ci.yml`), es gibt also keinen
geteilten Zustand zwischen Läufen. Lokal reicht ein Wegwerf-Container mit denselben Werten
und derselben Version wie in CI (dort `postgres:16-alpine`; bei Abweichung gilt `ci.yml`):

```bash
docker run --rm -d --name privasync-test-db   -e POSTGRES_USER=privasync -e POSTGRES_PASSWORD=ci-test-only -e POSTGRES_DB=privasync   -p 5432:5432 postgres:16-alpine

export TEST_DATABASE_URL=postgres://privasync:ci-test-only@localhost:5432/privasync
make test-server

docker stop privasync-test-db   # --rm entfernt den Container samt Daten
```

- Ein eigenes Make-Target dafür gibt es nicht; `make test-server` liest `TEST_DATABASE_URL`
  aus der Umgebung (die Tests lesen es sonst aus einer `.env` im Repo-Root).
- Der Container nutzt nur Wegwerf-Zugangsdaten. Nie gegen eine Datenbank mit echten Daten
  testen: die Tests schreiben in die Datenbank.
- Ist Port 5432 belegt, mit `-p 55432:5432` mappen und die URL anpassen.

## Testabdeckung

Coverage-Gate in CI (`.github/workflows/ci.yml`). Schwellwerte sind gemessene Baselines
(zuletzt 2026-08-05, Runde 6), keine Zielwerte; sie werden nur angehoben, nie
gesenkt. Die Server-Messung nutzt `-coverpkg=./...`, damit Code zählt, der nur indirekt über
andere Pakete getestet wird (z. B. Repository/Storage via Handler-Tests). Die Server-Zahl
misst den **testbaren Kern**: `cmd/*` und die reinen Decoder `thumbnail_3d.go` und
`thumbnail_extra.go` sind aus der Basis ausgenommen (ADR-0028).

| Komponente | Ist | Ziel | Schwelle in CI |
|---|---|---|---|
| Server (Go, `apps/server`) | 80.6 % (Kern, 2026-08-13) | ≥ 80 % (erreicht) | 80 % |
| Web (`apps/web`) | 74.2 % (Lines, 2026-08-05) | ≥ 70 % (erreicht) | 72 % |
| Desktop-TS (`apps/desktop`) | 85.9 % (Lines, 2026-08-02) | ≥ 70 % (erreicht) | 82 % |
| Desktop-Rust (`apps/desktop/src-tauri`) | 60.9 % (Lines, 2026-08-05) | ≥ 60 % (erreicht) | 58 % |
| `packages/ui` | 4.7 % (Lines) | — | 4 % |
| `packages/i18n` | 63.2 % (Lines) | — | 60 % |
| `packages/api-client` | 0 % (keine Tests) | — | 0 % |

Reports lokal: `make check-coverage-server`, `pnpm --filter <paket> exec vitest run --coverage`,
`make test-desktop-rust-coverage`.

## Konventionen

- **Sprache**: Code-Kommentare und Identifier sind englisch. Sichtbare Texte (UI, Mails)
  sind deutsch, mit echten Umlauten statt `ae`/`oe`/`ue`/`ss`. Im Web laufen alle
  sichtbaren Strings über `@privasync/i18n`.
- **Formatierung**: `gofmt` und `cargo fmt` nur auf Dateien anwenden, die du selbst
  änderst; kein repo-weites Formatieren, damit Diffs klein bleiben.
- **Generierter Code**: `packages/api-client/src/events.gen.ts` wird aus den Go-Structs
  erzeugt. Nach Änderungen an SSE-Events `make gen-events` ausführen und den Diff
  einchecken; die CI-Prüfung `gen-events-check` schlägt bei Drift fehl.
- **ADRs**: Neue Entscheidung heißt neue ADR unter `docs/adr/`. Überholte ADRs werden mit
  `Status: Superseded by ADR-XXXX` markiert, nicht umgeschrieben. Eine ADR beschreibt die
  gültige Entscheidung, nicht den Weg dorthin.
- **Glossar**: Domänenbegriffe wie in [`glossary.md`](glossary.md) verwenden, keine
  Synonyme.
- **Commits**: Kurze, aussagekräftige Messages; Verweise auf Issues im Body. Tray-Icon-PNGs
  unter `apps/desktop/src-tauri/icons/` zeigen lokal oft als geändert an, ohne zur Aufgabe
  zu gehören; sie nicht versehentlich mit einchecken.

## UI-Konventionen

- Gemeinsame Bausteine kommen aus `@privasync/ui`. Apps importieren nicht direkt aus Radix
  und kopieren keine Primitives.
- Dropdowns sind immer `Select` aus `@privasync/ui`, nie ein natives `<select>`.
  Aktionsmenüs nutzen `DropdownMenu`, Binärwerte `Switch`.
- Primitives sind i18n-frei und kennen keine Domänentypen; Texte liefert der Aufrufer.
- Farben und Abstände kommen aus den Tokens in `theme.css`, keine Hex-Werte in Komponenten.
- Tabellen sind sortierbar, wo Sortierung fachlich sinnvoll ist (Texte, Zahlen, Daten,
  Status), nicht bei reinen Aktions- oder Icon-Spalten. Im Web über `SortableTh` und
  `useTableSort`, der Zustand liegt in `localStorage`.
- Web: Die URL ist die Navigationswahrheit (`/files/<root>/<path>`, Suche als `?q=`).
  API-Aufrufe laufen ausschließlich über `apiClient`, kein `fetch` in Pages. Typen kommen
  aus `@privasync/api-client`.

## Hinweise zur Server-Konfiguration

Die Env-Var-Konvention (`PRIVASYNC_<SECTION>__<FIELD>`), WebDAV und die Office-Vorschau
stehen in [`deployment.md`](deployment.md). Es läuft immer genau eine Serverinstanz (ADR-0039).
