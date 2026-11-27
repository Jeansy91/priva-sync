# ADR-0015: Eine User-Freigabe pro Pfad (Grants) + Public-Dual-Link

**Status:** Accepted (2026-06-05)
**Datum:** 2026-06-05
**Supersedes:** [ADR-0014](0014-share-permission-update.md); revidiert das
Datenmodell aus [ADR-0010](0010-shared-mounts-recipient-view.md).

## Kontext

Heutiges Modell (im Code verifiziert):

- `shares` ist **eine Zeile pro Empfänger bzw. pro Token**:
  `(id, user_id, sync_root_id, target_user_id, path, share_type, token,
  password_hash, expires_at, can_write, created_at)`. Eine User-Freigabe an
  drei Personen sind **drei** Zeilen mit gleichem `path`. `can_write` und
  `expires_at` hängen an der Zeile.
- Der Empfänger-Anker ist die `shares.id`: `ListMountsForTarget` liefert sie
  als `SharedMount.ShareID`, der Desktop schlüsselt seine Mount-Zeilen darüber
  (ADR-0010/0011), `DeleteByTarget` löst über `id + target_user_id` aus.
- Public-Freigaben sind eine `shares`-Zeile mit **einem** `token`; `can_write`
  entscheidet, ob der Link Schreibzugriff hat — also nie Lese- **und**
  Schreiblink gleichzeitig.
- ADR-0014 führte `PATCH /shares/{id}` mit `{can_write}` ein, um genau **eine**
  Zeile umzuschalten.

Neue fachliche Anforderung:

1. Pro Verzeichnis/Datei genau **eine** User-Freigabe, die **bearbeitet**
   wird — Benutzer hinzufügen/entfernen, je Benutzer `read|write` und ein
   Ablaufdatum.
2. **Datei**-Freigaben haben **keinen** Schreibzugriff, nur Verzeichnisse.
3. Public-Freigabe erzeugt **zwei** Links: einen Schreib-, einen Lese-Link
   (bei Dateien nur den Lese-Link).
4. Freigaben-Seite modernisiert; Teilen-Dialog passend umgebaut.
5. Detailansicht listet **wer Zugriff hat** (Avatare), getrennt nach
   schreibend/lesend — auch für Dateien/Verzeichnisse **innerhalb** einer
   Freigabe (vererbter Zugriff).

Das aktuelle Zeilen-pro-Empfänger-Modell trägt das nicht: „eine bearbeitbare
Freigabe mit mehreren Personen" ist ein Aggregat mit N Grants, und „zwei
Public-Links" sprengt das Ein-Token-Feld.

## Entscheidung

### Datenmodell — Aggregat `shares` + `share_grants`

`shares` wird zur **Freigabe pro Pfad** (Aggregat), die Empfänger wandern in
eine Kind-Tabelle `share_grants`:

```sql
-- shares: ein Aggregat je (sync_root, path, share_type)
shares (
  id            uuid pk,
  user_id       uuid not null,        -- Eigentümer
  sync_root_id  uuid not null,
  path          text not null,
  share_type    text not null,        -- 'user' | 'public'
  read_token    text,                 -- nur public
  write_token   text,                 -- nur public + nur Verzeichnisse
  password_hash text,                 -- nur public (unverändert)
  expires_at    timestamptz,          -- Ablauf der Public-Links
  created_at    timestamptz not null,
  UNIQUE (sync_root_id, path, share_type)   -- 1 User- + 1 Public-Freigabe je Pfad
)

-- share_grants: ein Empfänger je User-Freigabe
share_grants (
  id             uuid pk,
  share_id       uuid not null references shares(id) on delete cascade,
  target_user_id uuid not null,
  can_write      bool not null default false,  -- bei Datei-Freigaben immer false
  expires_at     timestamptz,                  -- Ablauf je Empfänger
  created_at     timestamptz not null,
  UNIQUE (share_id, target_user_id)
)
```

- **Eine** User-Freigabe pro Pfad = ein `shares`-Row (`share_type='user'`) mit
  N `share_grants`. Hinzufügen/Entfernen = Grant-Insert/-Delete.
- `can_write`/`expires_at` **je Empfänger** liegen am Grant. Bei
  Datei-Freigaben erzwingt der Handler `can_write=false`.
- Public bleibt **ein** `shares`-Row (`share_type='public'`) mit
  `read_token` (immer) und `write_token` (nur Verzeichnisse). Beide Links
  teilen `expires_at`/`password_hash`.

#### Empfänger-Anker wandert auf den Grant

Der Mount-Anker des Empfängers ist künftig die **`share_grants.id`**, nicht
mehr `shares.id` — jeder Empfänger hat seinen eigenen Grant als persönliche
Mount-Zeile. `ListMountsForTarget` liefert die Grant-ID als
`SharedMount.ShareID`; `DeleteByTarget` löst über `grant.id + target_user_id`.
Damit bleibt die ADR-0010/0011-Empfängersicht **strukturgleich** — der Desktop
sieht weiter „eine ID pro Mount".

**Migration `026_share_grants.sql`** (ID-stabil, damit der Desktop nicht
prunet):

1. `share_grants` + neue `shares`-Spalten anlegen.
2. Je `share_type='user'`-Zeile: ein Grant anlegen mit **`grant.id = alte
   share.id`** (Anker bleibt stabil), `can_write`/`expires_at`/`target_user_id`
   übernommen. Pro `(sync_root, path)` die **älteste** Zeile als Aggregat-Row
   behalten, Duplikate (gleicher Pfad, weitere Empfänger) zu Grants am
   Aggregat zusammenführen und ihre alten `shares`-Rows löschen.
3. Je `share_type='public'`-Zeile: `read_token = altes token`; war
   `can_write=true`, zusätzlich `write_token = altes token` (alter Link
   behält seine Wirkung — kein toter Link). `target_user_id/can_write` an der
   Zeile entfallen.
4. Spalten `target_user_id`, `token`, `can_write` von `shares` droppen (nach
   Backfill).

### API — Grant-Sub-Resource statt Ein-Zeilen-PATCH

Kleine, einzeln testbare Endpunkte; jeder betrifft genau einen Empfänger →
saubere SSE/Mail je Ziel:

- `POST /sync-roots/{rootID}/shares` — **get-or-create** des Aggregats für
  `{path, share_type}`. Idempotent: existiert die Freigabe schon, wird sie
  zurückgegeben. Ersetzt das alte „Create legt sofort einen Empfänger an".
- `POST /shares/{shareID}/grants` — Empfänger hinzufügen
  `{target_user_id|target_username, can_write, expires_at}`. Bei
  Datei-Freigabe wird `can_write` ignoriert/auf false gezwungen.
- `PATCH /shares/{shareID}/grants/{userID}` — `{can_write?, expires_at?}`
  ändern (ersetzt ADR-0014 `PATCH /shares/{id}`).
- `DELETE /shares/{shareID}/grants/{userID}` — Empfänger entfernen.
- **Public-Links granular je Pfad** (Revision 2026-06-06): Lese- und
  Schreib-Link haben **unabhängige** Lebenszyklen auf **einer** public-Zeile.
  - `POST /sync-roots/{rootID}/public-links` `{path, kind: read|write}` —
    get-or-create der public-Zeile, erzeugt den Token der Art. `write` für
    Dateien → 400.
  - `DELETE /sync-roots/{rootID}/public-links?path=&kind=` — nullt den Token;
    sind beide weg, fällt die Zeile. Idempotent.
- `DELETE /shares/{shareID}` — ganze Freigabe inkl. Grants (Cascade) löschen.
- `GET /shares` — Aggregate des Eigentümers inkl. Grants + Public-Tokens.
- `GET /sync-roots/{rootID}/shares` — Pfad-Map für Badges, jetzt mit
  Grant-Liste + Public-Flags je Pfad.

#### Effektiver Zugriff für die Detailansicht

Neuer Read-Endpoint für „wer hat Zugriff", inkl. **Vererbung** auf Items
innerhalb einer Freigabe:

- `GET /sync-roots/{rootID}/access?path=<p>` → `{ writers: [UserSummary],
  readers: [UserSummary] }`. Aufgelöst aus den Grants der **nächstgelegenen
  Vorfahr-User-Freigabe** von `p` (inkl. `p` selbst). Abgelaufene Grants
  zählen nicht. So zeigt auch eine Datei unter einem freigegebenen Verzeichnis
  transparent ihre Zugriffsberechtigten.
- Mount-Variante für die Empfängersicht: `GET /shared-mounts/{id}/access`
  (liefert mind. Geber=write + eigenen Grant), Details in Slice-Planung.

### Public-Token-Auflösung

`FindByToken` matcht `read_token` **oder** `write_token`. Schreibzugriff
genau dann, wenn der getroffene Token der `write_token` ist. Datei-Shares
haben keinen `write_token` → kein anonymer Schreibpfad. Die anonymen
Schreib-Endpunkte (Upload in Public-Dir) sind Folge-Arbeit; diese ADR liefert
zunächst die zwei **Lese/Download**-tauglichen Links plus das Datenmodell für
Schreibzugriff.

### SSE — Events bleiben, Payload zeigt den Grant

`share_added` / `share_updated` / `share_revoked` bleiben erhalten, ihr
Payload beschreibt künftig den **Grant** des Empfängers (abgeleiteter
`SharedMount`/`ShareRef`). Damit gilt die ADR-0011-Desktop-Logik
(Pull=Wahrheit, SSE=Latenz) **unverändert** — pro betroffenem Empfänger ein
Event an Eigentümer und Ziel. Owner-seitig refresht die Freigaben-Seite über
dieselben Events (sie kennt die Grant→Pfad-Zuordnung).

### Web (Revision 2026-06-06: Seite statt Modal)

- **Kein Teilen-Modal mehr.** „Teilen" in der Files-Ansicht **navigiert** auf
  `/shares?root=&path=&dir=`. Ein Pfad ohne Freigabe öffnet dort eine **leere
  Gruppe direkt im Bearbeitungsmodus**.
- **Freigaben-Seite** = Konfigurator, nach Pfad gruppiert. Jede Zeile trägt im
  Header **Stift (Bearbeiten)** + **Mülltonne (ganze Freigabe löschen)**. Im
  Edit-Modus: Empfänger mit Zugriffsrecht (`read|write`, bei Dateien nur
  `read`) + Ablaufdatum hinzufügen/entfernen; Schreibrecht je Empfänger
  umschalten.
- **Öffentliche Links** als **zwei Zeilen** (lesend / schreibend, schreibend
  nur Verzeichnis): je „Erzeugen" → Link + Copy → „Löschen", unabhängig.
- **Detailansicht**: Avatar-Liste „Zugriff", getrennt schreibend/lesend, via
  `/access`-Endpoint — auch für Items innerhalb eines Mounts/einer Freigabe.

### Scope-Grenzen

- **Anonymer Schreib-Upload** über `write_token` (Public schreibend) ist
  Folge-Arbeit; diese ADR liefert Modell + Tokens, nicht den Upload-Flow.
- **Public-Passwort-UI** bleibt wie heute ungenutzt; Feld bleibt erhalten.
- **Desktop** ändert sich nur durch den stabil migrierten Grant-Anker — keine
  neue Desktop-Logik (ADR-0011 trägt unverändert).

## Konsequenzen

**Positiv**
- „Eine bearbeitbare Freigabe pro Pfad" wird ein echtes Aggregat; Hinzufügen/
  Entfernen/Recht/Ablauf je Empfänger sind kleine, unabhängige Operationen.
- Empfänger-Anker ID-stabil migriert → **kein** Desktop-Prune-Churn.
- Public-Dual-Link fällt natürlich aus `read_token`/`write_token`.
- Vererbter `/access` macht Zugriff überall transparent, auch tief im Baum.

**Negativ / offen**
- Spürbare Migration (Backfill + Spalten-Drop) und Anpassung von
  `ListMountsForTarget`, `ListBySyncRoot`, `FindByToken`, den Mail-/SSE-Pfaden.
- Mehr Endpunkte (Grant-Sub-Resource) statt eines PATCH.
- Anonymer Public-Schreibpfad und Public-Passwort-UI bleiben unerledigt.
- `GET /access` mit Vorfahr-Auflösung braucht einen effizienten Lookup
  (Pfad-Präfix), auf großen Roots zu beobachten (vgl. ADR-0008).
</content>
</invoke>
