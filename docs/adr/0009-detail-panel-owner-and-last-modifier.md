# ADR-0009: Eigentümer und letzter Bearbeiter in der Detailansicht

**Status:** Accepted (Stufe 1 — Owner-Embed in /sync-roots + Pillen-Design
in FileDetailPanel implementiert in 1e73e6c. Stufe 2 "letzter Bearbeiter"
wird in [ADR-0013](0013-last-modifier-tracking.md) eingelöst — der durch
Shared Mounts (ADR-0010) entstandene Multi-User-Schreibzugriff ist der
Trigger, auf den diese ADR gewartet hat.)
**Datum:** 2026-05-26

## Kontext

In der Detailansicht des Files-Browsers
([`FileDetailPanel`](../../apps/web/src/pages/files/FileDetailPanel.tsx))
sieht man Name, Größe, Pfad, Mtime, Mime — aber **nicht**, wem die Daten
gehören oder wer sie zuletzt angefasst hat.

Aktueller Stand der API:

- [`SyncRoot`](../../packages/api-client/src/types.ts) hat nur eine
  `user_id` — keinen Username und keine `avatar_url`.
- [`FileEntry`](../../packages/api-client/src/types.ts) hat keinen
  „letzter Bearbeiter"-Verweis. `updated_at` sagt _wann_, nicht _wer_.
- Sharing existiert pro Pfad ([`Share`](../../packages/api-client/src/types.ts)),
  aber es gibt **noch keine geteilten Sync-Roots im Sinne mehrerer
  bearbeitender Nutzer**. Der ganze Root gehört genau einem User; alle
  Schreibzugriffe stammen von ebendiesem User.
- Eine fertige [`UserAvatar`](../../apps/web/src/components/layout/AppShell.tsx)-
  Komponente existiert bereits (Initialen-Fallback, Blob-Loading aus
  `auth.fetchAvatar`).

Solange Sync-Roots single-user sind, ist „letzter Bearbeiter" immer
identisch mit „Eigentümer" — die Anzeige bringt keinen
Informationsgewinn. Sobald aber Multi-User-Sync-Roots eingeführt werden
(siehe noch zu erstellendes Issue), wird die Unterscheidung relevant:
„dieser Ordner gehört Alice, aber Bob hat zuletzt etwas drin geändert".

## Entscheidung

Wir bauen die Anzeige in **zwei Stufen**, beide vertikal-getest (TDD,
Backend zuerst):

### Stufe 1 — Eigentümer im SyncRoot

1. Server erweitert die `/sync-roots`-Response um ein eingebettetes
   `owner`-Objekt:

   ```jsonc
   {
     "id": "...",
     "name": "...",
     "path": "...",
     "owner": { "id": "...", "username": "...", "avatar_url": "..." },
     "created_at": "..."
   }
   ```

   Das vermeidet einen Round-Trip pro Root. `avatar_url` bleibt nullable.

2. `packages/api-client` zieht `owner: UserSummaryWithAvatar` ins
   `SyncRoot`-Type. Bestehender Typ `UserSummary` wird um `avatar_url`
   erweitert oder ein neuer Typ daneben angelegt — Entscheidung im
   Implementierungs-PR.

3. `FileDetailPanel` zeigt einen neuen Block „Eigentümer" mit
   `UserAvatar` (klein, ~24px) + Username. Sichtbar sowohl bei
   `kind: 'file'` als auch bei `kind: 'dir'`.

### Stufe 2 — Letzter Bearbeiter pro Datei (später)

Wird **erst sinnvoll** mit Multi-User-Sync-Roots. Skizze für die
spätere ADR/Implementierung:

- `FileEntry.last_modifier_id` (nullable) im DB-Schema.
- Server liefert `last_modifier: UserSummaryWithAvatar | null` analog
  zu `owner`.
- Detailansicht zeigt die Bearbeiter-Zeile **nur**, wenn
  `last_modifier` existiert _und_ vom Owner abweicht — sonst Rauschen.

Diese Stufe ist in dieser ADR bewusst _nicht_ entschieden, nur skizziert,
damit Stufe 1 nicht in eine Sackgasse läuft (`owner`-Shape muss zu
`last_modifier`-Shape passen).

## Konsequenzen

**Positiv**

- Detailansicht beantwortet die „wem gehört das?"-Frage ohne Umweg über
  die Settings.
- Owner-Daten sind eingebettet → keine zusätzlichen API-Calls beim
  Öffnen einer Datei.
- Wiederverwendung von `UserAvatar`; keine neue Komponente.
- Stufe-1-Shape ist forward-kompatibel zu Stufe 2 (gleicher
  `UserSummaryWithAvatar`-Typ).

**Negativ / offen**

- Solange Sync-Roots single-user sind, zeigt die Owner-Zeile für jeden
  eigenen Root den eingeloggten User selbst — gefühlt redundant. Wert
  entsteht erst mit geteilten Roots; vorher ist es „Konsistenz für
  später".
- `avatar_url` zwingt `/sync-roots` zu einem zusätzlichen Join auf die
  Users-Tabelle. Bei der bekannten Größenordnung (wenige Roots pro User)
  unkritisch.
- Stufe 2 erfordert ein DB-Migration auf `files.last_modifier_id` plus
  Sync-Engine-Anpassung (jeder Schreibvorgang muss den ausführenden
  User mitschreiben) — separater Aufwand, separate ADR.
