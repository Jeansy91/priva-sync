# ADR-0040: WebDAV authentifiziert über App-Passwörter, nicht über das Kontopasswort

**Status:** Accepted
**Datum:** 2026-08-24

## Kontext

Der WebDAV-Listener auf Port 8081 (ADR-0018) akzeptierte Basic-Auth mit dem
Kontopasswort. Damit war jedes Konto über diesen Port mit Benutzername und
Passwort vollständig erreichbar — inklusive Schreibzugriff über den Sync-
Coordinator.

Basic-Auth kann keinen zweiten Faktor abfragen. Für ein Konto mit aktivem TOTP
hiess das: der zweite Faktor galt auf `/api/v1/auth/login`, aber nicht auf
8081. Wer das Passwort hatte, war drin. Der zweite Faktor war damit keine
Sicherheitsmassnahme mehr, sondern Dekoration.

Erschwerend fehlte auf dem Port jede weitere Kontrolle: kein Lockout-Check,
kein `RecordLoginAttempt`, kein Rate-Limit, kein Audit-Event. Ein erfolgreicher
Einbruch hinterliess keine Spur.

## Entscheidung

Auf WebDAV gilt das Kontopasswort nicht mehr. Stattdessen **App-Passwörter**:

- Eigene Tabelle `app_passwords` (Migration 033), pro Gerät bzw. Programm
  erzeugt und **einzeln widerrufbar**, ohne das Konto anzufassen.
- Das Geheimnis erzeugt der Server (160 bit Zufall, base32, in Vierergruppen
  zur Übertragbarkeit) und zeigt es **genau einmal** an. Gespeichert wird nur
  SHA-256.
- **Kein Argon2id.** Gleiche Begründung wie bei den Backup-Codes: das
  Geheimnis ist nicht nutzergewählt und nicht ratbar. Ausserdem muss der Hash
  pro WebDAV-Request direkt nachschlagbar sein — mit Argon2id wären es sonst
  64 MiB und ~100 ms **pro gespeichertem Credential** und Request.
- Der präsentierte Benutzername muss dem Eigentümer entsprechen. Das ist keine
  Sicherheitsgrenze (das Geheimnis ist schon eine), sondern verhindert, dass
  ein vertipptes Konto stillschweigend funktioniert.
- Der Bearer-Pfad für den Desktop-Client bleibt unverändert.
- `last_used_at` wird bei jeder Nutzung gesetzt, damit der Eigentümer ein
  aktives Gerät von einem vergessenen unterscheiden kann, bevor er widerruft.
- Der Listener bekommt ein Rate-Limit (20 req/s, Burst 100) — locker genug für
  einen gemounteten Client, eng genug, dass Raten Zeit kostet.

## Konsequenzen

**Positiv**

- Der 2FA-Bypass ist zu. Ein Konto mit TOTP ist über 8081 nicht mehr mit dem
  Kontopasswort erreichbar — und über ein Konto ohne TOTP ebensowenig, weil
  die Regel nicht am 2FA-Status hängt. Eine Regel, die nur für 2FA-Konten
  gälte, würde in dem Moment aufhören zu schützen, in dem jemand 2FA abschaltet.
- Ein kompromittiertes Gerät kostet einen Widerruf, keine Passwortänderung.
- Ein Datenbank-Dump enthält keine wiederverwendbaren WebDAV-Credentials.

**Negativ**

- Bestehende WebDAV-Konfigurationen brechen. Wer rclone oder einen Mount mit
  dem Kontopasswort eingerichtet hat, muss ein Geräte-Passwort erzeugen und
  eintragen. Ein Migrationspfad ist bewusst nicht vorgesehen: einen Übergang,
  in dem das Kontopasswort weiter gilt, gibt es nicht, sonst bliebe der Bypass
  genau so lange offen.
- Eine zusätzliche Credential-Sorte, die Nutzer verstehen müssen. Die
  UI-Beschreibung sagt deshalb ausdrücklich, dass das Kontopasswort dort nicht
  funktioniert.

## Bewusst nicht getan: Kontosperre bei Fehlversuchen

Das ursprüngliche Ticket forderte, WebDAV-Fehlversuche in `login_attempts` zu
schreiben und den Lockout auslösen zu lassen. Mit App-Passwörtern ist die
Prämisse weggefallen: geraten wird nicht mehr ein nutzergewähltes Passwort,
sondern ein 160-bit-Zufallswert. Ein Lockout brächte dort nichts — würde aber
jedem erlauben, ein fremdes Konto per Fehlversuch-Spam vom WebDAV auszusperren.

Stattdessen: Rate-Limit pro Adresse (bremst), Audit-Events (macht sichtbar),
kein Kontolockout (kein DoS-Hebel).

## Verworfene Alternative

**Basic-Auth ersatzlos abschalten, nur Bearer zulassen.** Deutlich kleiner,
aber bricht rclone, den Windows-Explorer-Mount und jeden anderen
Standard-WebDAV-Client — also genau den Zweck, für den ADR-0018 den Listener
überhaupt gebaut hat.
