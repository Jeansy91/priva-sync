# ADR-0026: Passkey/WebAuthn-Login als eigenständige Anmeldemethode

**Status:** Accepted
**Datum:** 2026-07-30

## Kontext

Gefordert ist passwortlose Anmeldung via Passkey/WebAuthn neben
Passwort(+TOTP). Vor dieser Änderung existierte im Repo keinerlei
WebAuthn-Code — vollständige Neuentwicklung über Server (Go) und Web
(React). Bestehendes Muster für "zweiten Anmeldeweg" ist der
2FA-Session-Hand-off (`Login` → `Issue2FASessionToken` →
`Verify2FA` → `issueTokenPair`, `apps/server/internal/api/handler/auth_session.go`)
sowie der Setup/Confirm-Zweischritt von TOTP (`auth_totp.go`). Beide
mussten für Passkey adaptiert, nicht 1:1 übernommen werden, weil ein
Passkey kein zweiter Faktor zu einem Passwort ist, sondern ein eigener,
vollwertiger Anmeldeweg.

## Entscheidung

1. **Passkey ist eigenständige Login-Methode, kein zweiter Faktor.**
   `PasskeyLoginFinish` endet direkt in `AuthHandler.issueTokenPair` —
   durchläuft nicht den `2fa_session`-Pfad. Ein Nutzer mit Passkey
   meldet sich ohne Passwort/TOTP-Schritt an.

2. **Challenge-State im JWT, kein neuer Server-State.** Die WebAuthn
   `SessionData` (Challenge, RP-ID, erlaubte Credential-IDs) wird
   JSON-marshalt und in einem kurzlebigen `TokenTypePasskeyChallenge`-JWT
   transportiert (`internal/auth/jwt.go`, `PasskeyChallengeTTL` =
   2 Minuten) — gleiches Idiom wie `2fa_session`. Vermeidet Redis/
   In-Memory-Store für eine Ceremony, die typischerweise Sekunden dauert.

3. **Discoverable Credentials only (usernameless).** Der Login-Button
   ruft `navigator.credentials.get()` ohne Username auf; der Server nutzt
   `BeginDiscoverableLogin`/`ValidatePasskeyLogin` mit einem
   `DiscoverableUserHandler`, der den Nutzer über die
   WebAuthn-`credential_id` nachschlägt. Der von go-webauthn ebenfalls
   unterstützte Username-first-Pfad (`BeginLogin`/`ValidateLogin`) wird
   nicht gebaut — zweiter Code-Pfad ohne UX-Vorteil, da Passkeys gerade
   die Username-Eingabe überflüssig machen sollen.

4. **Der komplette `webauthn.Credential` wird als JSONB gespeichert**
   (`passkey_credentials.credential`), zusätzlich eine `credential_id
   BYTEA UNIQUE`-Spalte für den Lookup. Einzelne Spalten pro Feld hätten
   riskiert, gegen die interne Struct-Form der Library zu driften;
   das komplette Struct zu persistieren ist robuster und
   zukunftssicher gegen neue Felder der Library.

5. **"Mindestens eine Anmeldemethode bleibt bestehen" ist strukturell
   garantiert, nicht laufzeitgeprüft.** `users.password_hash` ist
   `NOT NULL`; einen "Passwort entfernen"-Flow gibt es nicht. Passkeys
   sind rein additiv — kein Extra-Guard nötig.

6. **Audit-Events folgen dem bestehenden `auth.*`-Präfix**
   (`auth.passkey_registered`, `auth.passkey_removed`,
   `auth.passkey_login`) statt der wörtlichen Issue-Schreibweise
   `passkey.*` — konsistent mit allen anderen Auth-Events
   (`auth.login.success`, `auth.2fa_enabled`, …).

7. **Desktop/Mobile bleiben bewusst außen vor** (Web-only, laut Anforderung
   explizit für Folgearbeit vorgesehen). Ein künftiger
   Desktop-Passkey-Flow würde am gleichen Refresh-Vertrag andocken, den
   ADR-0025 für den Desktop-API-Client etabliert hat.

## Konsequenzen

**Positiv:**

- Kein neuer Server-State/Store für Ceremony-Daten — Betriebsaufwand
  bleibt bei null.
- Ein Login-Endpunkt-Paar (`begin`/`finish`) statt zwei (mit/ohne
  Username) — kleinere Angriffsfläche, weniger zu testender Code.
- Speicherformat ist library-agnostisch stabil; ein Upgrade von
  `go-webauthn` auf eine neue Minor-Version mit zusätzlichen
  `Credential`-Feldern erfordert keine Migration.

**Negativ:**

- Volle Ceremony-Verifikation (echte Attestation/Assertion-Signaturen)
  ist ohne virtuellen Authenticator nicht in Go-Unit-Tests simulierbar.
  Die Tests decken Autorisierung, Scoping und Challenge-Token-Validierung
  ab; der register→login-Rundlauf wird manuell im Browser verifiziert
  (siehe Test-Plan in PR/Issue).
- Discoverable-only bedeutet: Nutzer mit mehreren Konten auf demselben
  Gerät verlassen sich auf die Browser/OS-Passkey-Auswahl, nicht auf
  eine App-seitige Username-Eingabe. Für PrivaSyncs Ein-Konto-pro-Nutzer-
  Realität kein praktischer Nachteil.
