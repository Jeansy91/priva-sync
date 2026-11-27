# ADR-0061: Geräte registrieren sich bei jedem Refresh, Löschen ist reine Buchführung

**Status:** Accepted
**Datum:** 2026-09-03

## Kontext

Die Geräte-Übersicht (`GET /api/v1/admin/clients`) wurde bisher ausschliesslich
aus `POST /auth/login` gespeist. Der Refresh-Pfad schickte nur den Header
`X-Client-Device` und hob damit `last_seen`/`last_ip` an.

Daraus folgen drei Lücken, die in der Praxis alle drei aufgetreten sind:

- **Zwei-Faktor-Logins registrieren nie.** `Verify2FA` nahm kein
  `client`-Objekt entgegen. Für jeden Nutzer mit aktivem TOTP endet der Login
  dort — sein Desktop-Client taucht in der Übersicht überhaupt nicht auf.
- **Die Client-Version veraltet.** Ein Desktop-Client aktualisiert sich im
  Betrieb und startet neu; die gespeicherte Session bleibt gültig, es folgt
  also nur ein Refresh, kein Login. Die Version in der Übersicht bleibt die
  von der letzten Anmeldung — bei einer monatelangen Session beliebig lange.
- **Sessions älter als die Registry bleiben unsichtbar.** `TouchLastSeen`
  liefert `ErrNotFound`, und der Aufrufer verwirft das bewusst. Ohne
  erzwungenen Re-Login gibt es keinen Weg, wie so eine Session je eine Row
  bekäme.

Gleichzeitig sammelt die Übersicht Einträge von Geräten, die es nicht mehr
gibt (Neuinstallation erzeugt eine neue `device_id`, Rechner ausgemustert).

## Entscheidung

**Die Identifikation reist auf jedem Auth-Pfad mit, der eine Session
begründet oder verlängert:** `login`, `2fa` und `refresh`. Beim Refresh als
optionaler Body `{"client": {…}}`; ist er vorhanden und gültig, läuft derselbe
`Upsert` wie beim Login, nicht der schmalere Touch.

Das ist der Punkt: **der Refresh ist der einzige Aufruf, den eine lange
laufende Session überhaupt macht.** Er ist deshalb der einzige Ort, an dem eine
nach dem Login geänderte Tatsache — die Version nach einem In-Place-Update —
den Server erreichen kann. Und weil `Upsert` einfügt statt nur zu
aktualisieren, heilt derselbe Pfad die beiden anderen Lücken mit: eine Session
ohne Row bekommt eine, ohne dass jemand sich neu anmelden muss.

Der Header `X-Client-Device` bleibt als Fallback bestehen — ein älterer Client
gegen einen neuen Server behält den Heartbeat, den er heute hat.

**`DELETE /api/v1/admin/clients/{id}` entfernt den Eintrag, sonst nichts.**
Kein Token wird widerrufen, keine Session beendet. Ein Gerät, das noch
angemeldet ist, erscheint beim nächsten Login oder Refresh wieder — genau
deshalb ist die Liste selbstreinigend: was verschwindet und wegbleibt, ist
tatsächlich weg, und was zurückkommt, war nie weg.

Verworfen: Löschen als Widerruf (Session-Ende) zu definieren. Das wäre eine
zweite, versteckte Bedeutung für einen Knopf, der in einer *Übersicht* sitzt,
und würde einen laufenden Sync ohne Vorwarnung abwürgen. Widerruf ist eine
eigene Handlung und gehört, wenn er kommt, an einen eigenen Knopf.

## Konsequenzen

**Positiv**

- Version, OS-Version und Gerätename sind so aktuell wie das jüngste
  Refresh-Intervall, nicht wie die letzte Anmeldung.
- Bestehende Sessions registrieren sich ohne Zutun nach; kein Migrationsschritt,
  kein erzwungener Re-Login.
- Ein Gerät hinter aktivem TOTP ist nicht länger unsichtbar.

**Negativ**

- Jeder Refresh trägt einen kleinen JSON-Body und löst statt eines `UPDATE`
  einen `INSERT … ON CONFLICT` aus. Bei Refresh-Intervallen in Minuten ist das
  gegenüber dem restlichen Sync-Verkehr nicht messbar.
- Der Client ruft pro Refresh `os_info::get()` auf.
- Ein Eintrag, den ein Admin löscht, während das Gerät noch synchronisiert,
  ist innerhalb eines Refresh-Intervalls wieder da. Das ist gewollt und steht
  so im Bestätigungsdialog.

## Referenzen

- ADR-0060 — jeder App-Start installiert selbst und startet neu; damit trifft
  die veraltete Version nach dieser Entscheidung jeden angemeldeten Client bei
  jedem Release, nicht nur den, der manuell aktualisiert hat
- ADR-0046 — Refresh-Rotation mit Kulanzfenster; der Heartbeat sitzt im selben
  Handler und hinter demselben Early-Return für Aufrufe im Kulanzfenster
- ADR-0029 — Handler-Repository-Seam; `AdminClientsHandler` hängt jetzt an
  `adminClientStore` statt am konkreten Repository
