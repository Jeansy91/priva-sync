# ADR-0013: Letzter Bearbeiter pro Datei (zieht ADR-0009 Stufe-2 vor)

**Status:** Proposed
**Datum:** 2026-05-28

## Kontext

[ADR-0009](0009-detail-panel-owner-and-last-modifier.md) hat die Detailansicht um eine „Eigentümer"-Zeile erweitert und die zweite Stufe — „letzter Bearbeiter pro Datei" — bewusst zurückgestellt. Begründung damals: solange ein Sync-Root single-user ist, ist der letzte Bearbeiter immer der Eigentümer; die Anzeige hätte keinen Informationsgewinn.

Mit [ADR-0010](0010-shared-mounts-recipient-view.md) ist diese Voraussetzung nicht mehr gegeben. Ein **schreibbarer** Shared Mount erlaubt einem zweiten User (dem Empfänger), Dateien in einem fremden Sync-Root zu mutieren — Upload, Move, Delete, Restore. Heute wird diese Mutation server-seitig unter dem Geber-User verbucht ([`UploadMountFile`](../../apps/server/internal/api/handler/shared_mounts.go) ruft den Coordinator mit `share.UserID` auf). Damit ist die Information „wer hat das wirklich angefasst?" bereits am Schreib-Pfad **vorhanden** (`claims.UserID`), wird aber sofort weggeworfen.

Konsequenz: Alice sieht heute nicht, dass Bob ihr `urlaub.jpg` über den Mount hochgeladen hat. Sie sieht es als „von Alice, weil im eigenen Quota verbucht". Das ist genau der Fall, den ADR-0009 als Trigger für Stufe-2 markiert hat — Multi-User-Schreibzugriff auf einen Root.

Wir ziehen Stufe-2 daher jetzt, als eigene ADR (nicht als Patch in 0009), weil DB-Migration plus Coordinator-Patch eigener Scope ist.

## Entscheidung

### DB

Neue nullable Spalte `files.last_modifier_user_id UUID NULL` (FK → `users.id`, `ON DELETE SET NULL`). Bestehende Rows bleiben `NULL`; UI rendert die Reihe nur, wenn beide (`owner`, `last_modifier`) gesetzt sind **und** divergieren.

Backfill ist nicht nötig — alte Rows haben keine zweite Hand am File berührt, also ist `NULL` semantisch korrekt.

### Coordinator

Jeder mutierende Pfad setzt `last_modifier_user_id` auf den **tatsächlichen Akteur** (`claims.UserID`), nicht auf den Storage-Owner (`share.UserID`):

- `UploadCmd`, `MoveCmd`, `DeleteCmd`, `RestoreCmd` bekommen ein zusätzliches `ActorUserID uuid.UUID`-Feld.
- Handler-Adapter befüllen es:
  - `/api/v1/sync-roots/{rootID}/files/...` → `ActorUserID = claims.UserID` (= `share.UserID`, weil eigener Root).
  - `/api/v1/shared-mounts/{shareID}/files/...` → `ActorUserID = claims.UserID` (= Empfänger, **nicht** `share.UserID`).
- Coordinator schreibt `ActorUserID` in `files.last_modifier_user_id` bei jedem `INSERT`/`UPDATE`.

### API

`FileEntry` (`packages/api-client/types.ts`) erhält:

```ts
last_modifier?: UserSummaryWithAvatar | null
```

Server-Endpoints, die Files zurückgeben, joinen analog zu `SyncRoot.owner` (ADR-0009) auf `users`:

- `GET /sync-roots/{id}/files`
- `GET /shared-mounts/{id}/files`
- `GET /shared-mounts/{id}/stats` (siehe ADR-0012, für File-Mount-Variante)
- `GET /files/search` (global)

### UI

`FileDetailPanel` rendert eine zusätzliche Owner-Pillen-Reihe mit Label „Letzte Änderung von" **nur**, wenn:

```ts
last_modifier && owner && last_modifier.id !== owner.id
```

Sonst Stille — keine Reihe, kein Platzhalter. Die Pille reuses [`UserAvatar`](../../apps/web/src/components/layout/AppShell.tsx) wie schon die Owner-Pille aus ADR-0009.

### Glossar

`docs/glossary.md` bekommt:

**Letzter Bearbeiter** (`last_modifier_user_id`):
User, der die letzte Mutation auf eine Datei ausgeführt hat — kann vom Eigentümer abweichen, wenn ein Empfänger über einen schreibbaren Shared Mount geschrieben hat. Nullable; alte Datensätze (vor ADR-0013) tragen `NULL`.
_Avoid_: Editor, Modifier, Author

## Konsequenzen

### Positiv

- Alice sieht Bobs Mount-Edits in ihrer eigenen Detail-Ansicht. Audit-Wert ohne separates Audit-Log-UI.
- Bob sieht in seinen Mount-Files, dass er selbst die letzte Hand angelegt hat — bestätigt Schreib-Rechte sichtbar.
- Shape ist forward-kompatibel zu echten Multi-User-Sync-Roots (falls die später kommen): keine Mount-spezifische Spalte, sondern `last_modifier_user_id` direkt am File.
- Conflict-Resolution kann später dieselbe Spalte nutzen („wer hat die loser-Kopie produziert?").

### Negativ / offen

- DB-Migration: zusätzliche Spalte + FK auf `users`. Bei großen `files`-Tabellen ist das eine Online-Migration; Postgres kann das mit `ADD COLUMN` ohne Lock-Storm machen, der FK-Check ist initial leer.
- Jeder Coordinator-Pfad muss `ActorUserID` setzen — vergessene Stelle führt zu `NULL` (sicher, aber Informationsverlust). Wird über Type-Required-Argument erzwungen, nicht über Default.
- Alle handler-tests müssen einen `ActorUserID`-Wert mitliefern. Aufwand mechanisch, ohne logische Konsequenz.
- Sync-Engine (Desktop, ADR-0011) muss `last_modifier` bei eingehenden Push-Events ignorieren, bis das Feld dort sinnvoll wird; ändert nichts an der Sync-Semantik.

### Beziehung zu ADR-0009

ADR-0009 bleibt als Stufe-1-Beschluss in Kraft. Status-Suffix wird auf
„Stufe-2 wird in ADR-0013 implementiert" aktualisiert. Diese ADR
ersetzt also nicht ADR-0009, sie löst dort die offen gehaltene
Stufe-2-Skizze ein.
