# Security Policy

## Supported Versions

Only the latest release (and the `latest` image on ghcr.io) receives security fixes. Desktop clients update themselves from the server, so keep your server up to date.

## Reporting a Vulnerability

Please **do not** open a public issue for security problems.

- Use **Report a vulnerability** in the repository's **Security** tab to send a private report: <https://github.com/Jeansy91/priva-sync/security/advisories/new>
- If that is not available, contact the maintainer [@Jeansy91](https://github.com/Jeansy91) directly on GitHub.

Please include what is affected, how to reproduce it, and the impact you expect. You will get an answer within 7 days.

## Automated Checks

Every pull request and every push to `main` runs the security gates in `.github/workflows/ci.yml`:

- `gosec` for the Go server and `semgrep` for static analysis
- `gitleaks` scans for committed secrets
- `cargo audit` and `pnpm audit` check the dependencies for known vulnerabilities
- `trivy` scans the server image

Dependabot (`.github/dependabot.yml`) keeps dependencies and GitHub Actions up to date. The security audit is a release gate, see [ADR-0007](docs/adr/0007-security-audit-release-gate.md).

## Operating PrivaSync Securely

- Run PrivaSync only behind HTTPS, for example with a reverse proxy such as Caddy or Nginx.
- Set a strong, random `JWT_SECRET` (at least 32 characters, for example from `openssl rand -hex 32`) and a strong `DB_PASSWORD`. Never commit `.env`; it is listed in `.gitignore`.
- Behind a reverse proxy, set `TRUSTED_PROXIES` to the proxy's network. Otherwise rate limit, login lockout and audit log see every user under the proxy address.
- Enable two-factor authentication (TOTP) or passkeys for accounts. WebDAV does not accept the account password; use per-device app passwords ([ADR-0040](docs/adr/0040-webdav-app-passwords.md)).
- Run exactly one server instance ([ADR-0039](docs/adr/0039-single-server-instance.md)).
- Keep the Tauri updater signing key private and backed up, see [docs/releasing.md](docs/releasing.md#signing--trust).
