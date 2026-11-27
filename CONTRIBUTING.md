# Contributing to PrivaSync

Thanks for your interest in PrivaSync! Bug reports, ideas and pull requests are welcome.

## Reporting bugs and suggesting features

- Search the [existing issues](https://github.com/Jeansy91/priva-sync/issues) first.
- Use the issue templates for bug reports and feature requests.
- **Security problems** must not be reported in a public issue. Follow [SECURITY.md](SECURITY.md) instead.

## Development setup

You need Go (version in `apps/server/go.mod`), Node.js with pnpm, the Rust toolchain (for the desktop client) and PostgreSQL. Always use `pnpm`, never `npm` or `yarn`.

```bash
pnpm install
cp .env.example .env     # set DB_HOST, DB_PASSWORD and JWT_SECRET
make dev-server          # Go server
make dev-web             # Vite dev server
pnpm --filter @privasync/desktop tauri dev
```

[docs/development.md](docs/development.md) describes the setup, all commands and the conventions in detail. The technical documentation is written in German. `make help` lists all `make` targets.

Run the tests before you open a pull request:

```bash
make test-server                        # Go tests (serial, they share one PostgreSQL database)
make typecheck                          # TypeScript check of all packages
make lint-server                        # go vet
pnpm --filter @privasync/web test       # web (Vitest)
pnpm --filter @privasync/desktop test   # desktop frontend (Vitest)
cd apps/desktop/src-tauri && cargo test # desktop Rust code
```

CI enforces coverage thresholds. They are measured baselines: they are only ever raised, never lowered (see [Testabdeckung](docs/development.md#testabdeckung)).

## Code guidelines

- **Language:** code comments and identifiers are in English.
- **User-facing text** goes through `@privasync/i18n`. Do not hard-code strings in components.
- **Formatting:** only format files you changed yourself, with `gofmt -w <file>` for Go and `rustfmt <file>` for Rust. Never run `cargo fmt` on the whole crate and never format the whole repository, so diffs stay small.
- **Generated code:** `packages/api-client/src/events.gen.ts` is generated from the Go SSE structs. After changing them run `make gen-events` and commit the result. CI fails on drift.
- **Shared UI:** use components from `@privasync/ui`, never copy primitives or import Radix directly.
- **Terms:** use the terms from the [glossary](docs/glossary.md).
- **Dependencies:** add new ones only when necessary.
- **Failing tests** are fixed before the commit, even if they were already failing.

### Architecture decisions

A design or architecture decision gets an Architecture Decision Record (ADR) in [docs/adr/](docs/adr/). Format, numbering and index are described in [docs/adr/README.md](docs/adr/README.md). A new decision means a new ADR. An outdated ADR is marked `Status: Superseded by ADR-XXXX` and not rewritten.

## Commit messages

PrivaSync uses [Conventional Commits](https://www.conventionalcommits.org/):

- `feat: ...` for a new feature
- `fix: ...` for a bug fix
- `docs:`, `test:`, `ci:`, `style:`, `chore:`, `refactor:` for other changes
- add `!` after the type (for example `feat!: ...`) for breaking changes

Reference issues in the commit body. Releases are triggered by a version bump, see [docs/releasing.md](docs/releasing.md).

## Pull requests

1. Fork the repository (or create a branch if you have access) and branch off `main`.
2. Make your change, run the tests and update the documentation if needed.
3. Open a pull request and fill in the template. The title follows the commit message format above.
4. CI must pass (tests with coverage gates, security scans and the image build).

## License

By contributing you agree that your contribution is licensed under the [AGPL-3.0](LICENSE), like the rest of the project.
