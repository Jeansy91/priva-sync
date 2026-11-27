## What does this change?

<!-- Short description and the reason for the change. Link related issues, e.g. "Closes #12". -->

## How was it tested?

<!-- Commands you ran, screenshots for UI changes. -->

## Checklist

- [ ] The PR title follows [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:` ...)
- [ ] Tests pass (`make test-server`, `pnpm --filter @privasync/web test`, `pnpm --filter @privasync/desktop test`, `cargo test` as far as touched)
- [ ] I only formatted files I changed (no `cargo fmt` on the whole crate)
- [ ] New user-facing text goes through `@privasync/i18n`
- [ ] `make gen-events` was run if Go SSE structs changed
- [ ] A new ADR in `docs/adr/` exists for architecture decisions
- [ ] README or docs updated if configuration or behaviour changed
