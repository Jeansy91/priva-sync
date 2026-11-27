.PHONY: dev dev-db build build-server build-web build-desktop \
        desktop security-audit typecheck clean help release \
        test-server test-server-coverage check-coverage-server test-desktop-rust-coverage

# ─── Entwicklung ─────────────────────────────────────────────────────────────

## Lädt Variablen aus .env (wird von dev-server genutzt)
-include .env
export

## Startet Go-Server (DB-Host, Passwörter und SMTP kommen aus .env)
dev-server:
	@test -n "$(DB_HOST)"      || (echo "Fehler: DB_HOST fehlt in .env" && exit 1)
	@test -n "$(DB_PASSWORD)"  || (echo "Fehler: DB_PASSWORD fehlt in .env" && exit 1)
	@test -n "$(JWT_SECRET)"   || (echo "Fehler: JWT_SECRET fehlt in .env" && exit 1)
	mkdir -p apps/server/data
	cd apps/server && \
	  PRIVASYNC_DATABASE__URL="postgres://privasync:$(DB_PASSWORD)@$(DB_HOST):5432/privasync" \
	  PRIVASYNC_AUTH__JWT_SECRET="$(JWT_SECRET)" \
	  PRIVASYNC_SMTP__HOST="$(SMTP_HOST)" \
	  PRIVASYNC_SMTP__PORT="$(SMTP_PORT)" \
	  PRIVASYNC_SMTP__USERNAME="$(SMTP_USER)" \
	  PRIVASYNC_SMTP__PASSWORD="$(SMTP_PASSWORD)" \
	  PRIVASYNC_SMTP__FROM_NAME="$(SMTP_FROM_NAME)" \
	  PRIVASYNC_SMTP__FROM_EMAIL="$(SMTP_FROM_EMAIL)" \
	  PRIVASYNC_SMTP__USE_TLS="$(SMTP_USE_TLS)" \
	  PRIVASYNC_SMTP__ENABLED="true" \
	  go run ./cmd/server -config config.dev.yaml

## Startet Web-Frontend im Dev-Modus
dev-web:
	pnpm --filter @privasync/web dev

## Startet Server + Web gleichzeitig (benötigt: brew install overmind oder foreman)
dev:
	@command -v overmind >/dev/null 2>&1 && overmind start -f Procfile \
	  || echo "Bitte 'make dev-server' und 'make dev-web' in separaten Terminals starten"

# ─── Build ───────────────────────────────────────────────────────────────────

## Baut den Go-Server (Binary)
build-server:
	cd apps/server && go build -ldflags="-w -s" -o ../../dist/server ./cmd/server

## Baut das Web-Frontend
build-web:
	pnpm --filter @privasync/web build

## Baut die Tauri Desktop-App
build-desktop:
	pnpm --filter @privasync/desktop tauri build

## Baut alle Komponenten
build: build-server build-web

## Baut Docker-Images und startet die Anwendung
up:
	@test -f .env || (echo "Fehler: .env fehlt – bitte aus .env.example erstellen" && exit 1)
	docker compose up --build -d
	@echo "PrivaSync läuft auf http://localhost:8080"

## Stoppt alle Container
down:
	docker compose down

# ─── Qualität ────────────────────────────────────────────────────────────────

## TypeScript-Typprüfung (alle Pakete)
typecheck:
	pnpm --filter "@privasync/*" typecheck

## Go-Tests. -p 1: tests share one Postgres DB across packages -- parallel
## packages cause interference (counter deltas, file renames, outbox claims)
## -- run serialized.
test-server:
	cd apps/server && go test ./... -p 1 -timeout 20m

## Go tests with coverage profile + HTML report. -p 1: tests share one
## Postgres DB across packages -- parallel packages cause interference
## (counter deltas, file renames) -- run serialized. -coverpkg=./...: credits
## code exercised indirectly through other packages' tests (e.g. repository/
## storage code driven by handler integration tests) -- otherwise those
## packages look untested even though the test suite does reach them.
test-server-coverage:
	cd apps/server && go test ./... -p 1 -timeout 20m -coverpkg=./... -coverprofile=coverage.out -covermode=atomic
	cd apps/server && go tool cover -html=coverage.out -o coverage.html

## Checks server coverage against the threshold (default 60%, see Issue #85 baseline)
COVERAGE_THRESHOLD_SERVER ?= 80
check-coverage-server: test-server-coverage
	cd apps/server && ../../scripts/check-coverage-go.sh coverage.out $(COVERAGE_THRESHOLD_SERVER)

## Rust coverage for the Tauri desktop app (requires: cargo install cargo-llvm-cov --locked)
test-desktop-rust-coverage:
	cd apps/desktop/src-tauri && cargo llvm-cov --summary-only

## Generiert TypeScript-Types fuer SSE-Events aus den Go-Structs
## (tygo muss installiert sein: go install github.com/gzuidhof/tygo@latest)
gen-events:
	@command -v tygo >/dev/null 2>&1 || (echo "tygo fehlt: go install github.com/gzuidhof/tygo@latest" && exit 1)
	cd apps/server && tygo generate --config ../../tygo.yaml
	@echo "events.gen.ts aktualisiert."

## CI-Gate: schlaegt fehl, wenn events.gen.ts nicht im Sync mit den Go-Structs ist.
gen-events-check:
	@$(MAKE) gen-events
	@git diff --quiet -- packages/api-client/src/events.gen.ts || (echo "FEHLER: events.gen.ts ist nicht im Sync. Bitte 'make gen-events' lokal aufrufen und committen." && git --no-pager diff -- packages/api-client/src/events.gen.ts && exit 1)

## Installiert .git/hooks/pre-commit (regeneriert events.gen.ts vor Commit)
install-hooks:
	@scripts/install-hooks.sh

## Go vet + staticcheck
lint-server:
	cd apps/server && go vet ./...

## Security-Audit als Release-Gate (SAST, Container, Secrets, DAST) — siehe ADR-0007
security-audit:
	@scripts/security-audit.sh

# ─── Release ─────────────────────────────────────────────────────────────────

## Bumpt Version, commitet und pusht den Branch. Aufruf: make release VERSION=0.2.0
# No tag here: the version bump *is* the release trigger. CI runs the gates,
# tags the commit and publishes it, all in the one run this push starts
# (ADR-0054). Pushing the tag from here put two runs of the same commit on one
# runner at the same time, and the loser of the race took the release down with
# it -- twice, at v0.7.1 and v0.7.2, once in a shared runner action cache and once
# in a Postgres-backed test.
release:
	@test -n "$(VERSION)" || (echo "Usage: make release VERSION=x.y.z" && exit 1)
	@echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$' || (echo "Fehler: VERSION muss semver x.y.z entsprechen" && exit 1)
	@test -z "$$(git status --porcelain)" || (echo "Fehler: Arbeitsverzeichnis nicht sauber - erst committen oder stashen" && exit 1)
	@scripts/bump-version.sh $(VERSION)
	git add -u
	git commit -m "chore: release $(VERSION)"
	git push origin HEAD
	@echo
	@echo "Gepusht. CI setzt v$(VERSION), sobald alle Gates grün sind,"
	@echo "und baut und publiziert im selben Lauf."

# ─── Aufräumen ───────────────────────────────────────────────────────────────

## Löscht Build-Artefakte
clean:
	rm -rf dist/
	pnpm --filter @privasync/web exec -- rm -rf dist
	cd apps/server && go clean

# ─── Hilfe ───────────────────────────────────────────────────────────────────

help:
	@grep -E '^##' Makefile | sed 's/## //'
