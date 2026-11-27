# ADR-0010: Shared Mounts — Empfänger-Sicht auf eingehende user-Shares

**Status:** Accepted (Stufe 1 — Server-API + Web-UI implementiert in
1e73e6c..3b4e7e0)
**Datum:** 2026-05-27

## Kontext

Heute existiert die `shares`-Tabelle mit zwei Typen (`public`, `user`)
und ein Endpoint [`ListByUser`](../../apps/server/internal/repository/share.go),
der jedoch nur Shares zurückgibt, die der Caller **selbst erstellt** hat
(`WHERE user_id = $1`). Folgen:

- **Empfänger sehen nichts.** Wer in `target_user_id` steht, bekommt zwar
  eine Mail bei der Freigabe, hat aber keine UI, um zugeteilte Pfade
  einzusehen.
- **Im Files-Browser ist nicht erkennbar**, dass eine Datei oder ein
  Verzeichnis bereits freigegeben ist — Geber sieht es nur über die
  separate `/shares`-Seite.

Konsequenzen: Doppel-Freigaben, vergessene Freigaben, schlechtes
Sicherheits-Bewusstsein, keine Möglichkeit für Empfänger,
Mount-ähnliche Workflows zu fahren.

Die `Share`- und `Shared Mount`-Begriffe sind nun in
[`docs/glossary.md`](../glossary.md) festgeklopft.

## Entscheidung

Wir führen das Konzept **Shared Mount** ein: empfängerseitige Sicht auf
eine `user`-Share-Row. Web + Server bilden Stufe 1; Desktop-Sync ist
explizit **nicht** Teil dieser ADR (siehe Stufe 2 unten).

### Datenmodell

- Keine neue Tabelle. Ein Shared Mount ist eine **Projektion** der
  bestehenden `shares`-Row mit `share_type = 'user'` und
  `target_user_id = caller`. ID des Mounts = `share.id`.
- `public`-Shares erzeugen **keinen** Mount (kein Adressat).
- 1:1 Share ↔ Shared Mount. Teilt Alice zwei Pfade desselben Sync Roots
  an Bob, sieht Bob zwei eigenständige Mounts.

### Permissions

- `can_write = true` → Empfänger hat **vollen R/W innerhalb
  `share.path`** (Upload, Move, Delete, Restore wie eigener Root).
- Quota-Buchungen laufen auf den **Sync-Root-Eigentümer** (Alice).
  Empfänger hat keine eigene Quota-Tasche für Mount-Inhalte.
- **Trash** des Mount-Inhalts gehört dem Sync-Root-Eigentümer: Löschen
  via Mount schiebt nach Alices Trash, Bob hat darauf keinen Zugriff.
- **Re-Sharing ist verboten**: `POST /shares` prüft, dass Caller den
  Sync Root besitzt — Mount-Inhaber dürfen nicht weiterverteilen.
- **Conflict-Semantik**: unverändert (ADR-0003 Loser-Kopie). Conflict-
  Row und Loser-Path liegen in Alices Sync Root, zählen auf ihre Quota.
  Empfänger sieht den Conflict in seinem Mount und kann ihn auflösen
  (wenn `can_write`).

### Server-API

Eigener Namespace, sauber getrennt von eigenen Sync Roots:

```
GET    /api/v1/shared-mounts                       — alle Mounts (target_user_id = caller)
GET    /api/v1/shared-mounts/{shareID}/files       — Listing analog zu /sync-roots/{id}/files
GET    /api/v1/shared-mounts/{shareID}/download    — Datei-Download
POST   /api/v1/shared-mounts/{shareID}/files       — Upload (nur wenn can_write)
PATCH  /api/v1/shared-mounts/{shareID}/files       — Move/Rename (nur wenn can_write)
DELETE /api/v1/shared-mounts/{shareID}/files       — Delete (in Trash des Eigentümers)
```

Jede Route validiert in Reihenfolge:

1. Share existiert + `target_user_id = caller`
2. `expires_at` nicht erreicht
3. Bei Schreib-Routen: `can_write = true`
4. Sub-Pfad bleibt innerhalb `share.path` (analog `isUnderShareRoot`)

Permission-Logik bleibt in der Mount-Handler-Schicht; klassische
File-Handler bleiben unangefasst, Sicherheits-Audit-Oberfläche klein.

Die Mount-Response enthält Geber-Profil eingebettet (siehe ADR-0009):

```jsonc
{
  "id": "<share.id>",
  "name": "2024",                  // basename(share.path) — bei Datei-Share Dateiname
  "path": "/Photos/2024",          // share.path im Geber-Root, nur als Hinweis
  "is_file": false,                // true wenn share.path eine Datei ist
  "can_write": true,
  "expires_at": null,
  "owner": { "id": "...", "username": "alice", "avatar_url": "..." }
}
```

### Web-UI

**Sidebar.** Sync-Root-Liste gruppiert: „Eigene" + „Geteilt mit mir".
Innerhalb der Mount-Gruppe: Label = letzter Pfadteil von `share.path`,
Avatar des Gebers daneben (wiederverwendet aus ADR-0009 / `UserAvatar`).

**Datei-Mounts.** `is_file = true` → Klick öffnet direkt das
FileDetailPanel auf der einen Datei, kein Listing dazwischen.

**Badges im Geber-Browser.** Geber sieht Badges nur auf der **exakten**
Share-Pfad-Zeile (nicht auf Eltern, nicht auf Kindern). Zwei Icons:
*Personen* (user-Share) und *Kette* (public/Token-Share); bei beiden
gleichzeitig erscheinen beide. Klick auf Badge → Navigation zur
`/shares`-Seite, dort gefiltert auf den Pfad.

**Badges im Empfänger-Browser.** Innerhalb eines Mounts werden **keine**
Badges gezeigt — der gesamte Mount ist implizit freigegeben, alles
Weitere wäre Rauschen.

**Datenquelle Badge.** Ein zusätzlicher Call `GET /sync-roots/{id}/shares`
(neu: gibt alle Shares **dieses** Roots zurück) liefert eine Map<path,
ShareInfo[]>, die das Frontend clientseitig pro Listing-Page matcht.
Updates per SSE `ShareAdded`/`ShareRevoked` (existieren bereits).

### Globale Suche

Die globale Suche
schließt Shared Mounts mit ein. Server-Suche
erweitert um Union: eigene `sync_roots` UND `files` aus Sync Roots, wo
ein aktiver `user`-Share auf den Caller zeigt (mit Pfad-Filter auf
`share.path`). Treffer aus Mounts liefern den Mount-Kontext im Resultat
(`mount_id` + Mount-Label), nicht den fremden Sync-Root-Namen.

### Lifecycle

- **DELETE /shares/{id}** publisht (bereits) `ShareRevoked` an
  `target_user_id`. Web reagiert: Mount aus Sidebar entfernen, bei
  aktiver Mount-View Toast + Redirect zur Files-Index.
- **Sync-Root-Löschung** kaskadiert Shares (bereits). Erweiterung:
  `DeleteSyncRootTx` sammelt `target_user_id`s und publisht
  `ShareRevoked` pro Mount-Empfänger.
- **Ablauf (`expires_at`)** schließt den Mount serverseitig durch
  Permission-Check (alle Mount-Routes 410); zusätzlich Cron-Mail an
  Empfänger ist optional, **nicht** Pflicht dieses ADR.
- **Widerrufs-Mail.** Bei explizitem `DELETE` (nicht bei Ablauf, nicht
  bei Root-Löschung) wird zusätzlich eine Mail an den Empfänger
  geschickt — analog zur bestehenden Share-Erstellungs-Mail.

### Stufe 2 — Desktop-Sync (eigenes ADR)

Skizze, damit Stufe 1 nicht in eine Sackgasse läuft:

- Bobs Desktop kann einen Shared Mount als zusätzlichen Sync-Ordner
  hinzufügen (lokales Verzeichnis ↔ `/shared-mounts/{id}`).
- Schreibvorgänge nutzen Bobs Token, Server bucht auf Alices Quota.
- Bei Widerruf verschwindet der Mount aus der Konfiguration; **lokale
  Dateien bleiben auf der Platte** (Bob behält die Bytes, der
  Server-Bezug ist gekappt).
- Cloud-Files-WinRT (ADR-0005), Conflict-Ownership-Details und die
  Konfigurations-UI für Mount-Pfade gehören in eine eigene ADR-0011.

## Konsequenzen

**Positiv**

- Empfänger sieht freigegebene Inhalte ohne Umweg über Mails oder
  Geber-Tickets.
- Geber erkennt im Files-Browser auf einen Blick, was nach außen geht
  — schließt das Sicherheits-Awareness-Gap.
- Permission-Modell bleibt einfach: Eigentum am Sync Root entscheidet
  alles (Quota, Trash, Re-Share, Conflict-Ownership). Kein Mischmodell.
- API-Namespace `/shared-mounts/*` macht Permission-Boundary explizit
  — leichter zu auditieren.
- 1:1 Share ↔ Mount minimiert Modell-Komplexität; keine impliziten
  Hierarchien zwischen Mounts.

**Negativ / offen**

- Vier neue Endpoint-Familien plus Search-Erweiterung — größere
  Implementation als ADR-0009.
- Server-Suche (Stufe 1 von ADR-0008) muss die Mount-Union mit-tragen,
  ohne die Million-File-Performance-Garantien zu brechen. Concrete
  Indizes/Query-Plan offen bis Implementation.
- Permission-Boundary wandert in den Handler; ein Bug dort öffnet
  fremden Sync-Root-Inhalt. Mitigation: dedizierte Handler-Tests, die
  jeden Pfad einzeln gegen target-mismatch und Sub-Pfad-Escape prüfen.
- Desktop-Stufe ist explizit verschoben — bis ADR-0011 existiert, ist
  die Web-Sicht autoritativ und kann sich von einer späteren Desktop-
  Implementation in Permissions oder Conflict-Sicht unterscheiden.
