# ADR-0014: Share-Permission-Wechsel ohne Widerruf

**Status:** Superseded by [ADR-0015](0015-share-grants-and-public-dual-link.md)
**Datum:** 2026-06-03

## Kontext

[ADR-0010](0010-shared-mounts-recipient-view.md) führt user-Shares mit
`can_write` ein; [ADR-0011](0011-shared-mounts-desktop-sync.md) synct sie auf
den Desktop und klammert dabei einen Punkt **ausdrücklich** aus (dort
„Negativ / offen"):

> `can_write`-Updates (Permission-Wechsel ohne Widerruf+Neuanlage) brauchen
> ein neues SSE-Event + Update-API — **nicht** Teil dieser ADR.

Heutiger Stand (im Code verifiziert):

- `shares.go` kennt nur **Create** (`POST .../shares`) und **Delete**
  (`DELETE /shares/{id}`). Es gibt **keinen** Update-Pfad — `can_write` lässt
  sich nur über Widerruf + Neuanlage ändern. Für Bob heißt das: Mount fällt
  aus `GET /shared-mounts`, Prune greift (CF weg, Bytes bleiben), und er muss
  den Mount neu aktivieren samt Pfadwahl. Ein reiner Schreibrecht-Toggle wirkt
  so wie ein Komplett-Entzug — schlechte UX.
- SSE kennt `share_added`/`share_revoked`, aber **kein** `share_updated`.
- Desktop: `read_only` einer Mount-Zeile wird **einmalig** bei der Aktivierung
  aus `!can_write` gesetzt (`mount_commands.rs`) und danach **nie**
  aufgefrischt. Der Pro-Cycle-Pull (`prune_orphaned_sync_roots`) zieht zwar
  `GET /shared-mounts` inkl. `can_write`, nutzt davon aber **nur die IDs** für
  die Prune-Entscheidung und verwirft `can_write`.

## Entscheidung

### Server — PATCH statt Revoke+Recreate

Neuer Endpoint `PATCH /api/v1/shares/{shareID}` mit Body `{ "can_write": bool }`.

- **Owner-only:** nur der Share-Eigentümer (`share.user_id == claims.UserID`)
  darf ändern; sonst 404 (kein Existenz-Leak, analog Delete-Semantik).
- Repo-Methode `UpdateCanWrite(shareID, userID, canWrite)` setzt das Feld via
  `UPDATE ... WHERE id = $1 AND user_id = $2` und gibt die aktualisierte Row
  zurück (`RowsAffected()==0` → `ErrNotFound`).
- Antwort: aktualisierte `shareResponse` (200), damit Web/Clients die Row ohne
  Re-Fetch patchen können — symmetrisch zu Create.
- Emittiert neues SSE-Event **`ShareUpdated`** (Payload = vollständige
  `ShareRef`, identische Form wie `ShareAdded`) an **Owner und Target**, damit
  beide Seiten ihre Share-Liste im Gleichschritt patchen.
- Audit-Log-Eintrag analog Create/Delete.

`can_write` ist vorerst das **einzige** mutable Feld. `expires_at`/`path`/
Re-Targeting bleiben außen vor (eigene Entscheidung, falls je gebraucht) —
hält den Endpoint klein und die Auth-Fläche überschaubar.

### Desktop — Pull autoritativ, SSE nur Latenz (wie ADR-0011)

Konsistent mit der ADR-0011-Philosophie „Pull = Wahrheit, SSE = Latenz":

1. **Pull-Reconcile ist autoritativ.** Der Pro-Cycle-Pull von
   `GET /shared-mounts` vergleicht für jede **lokal aktivierte** Mount-Zeile
   `!can_write` gegen das persistierte `read_only`. Bei Differenz:
   `upsert_sync_root` mit neuem `read_only`, CF-RO-OS-Attribut nachziehen, und
   bei `write → read-only` eine Notification („Schreibrecht für X entzogen").
   Das ist die Garantie — robust gegen verpasste Events.
2. **SSE `share_updated` ist reine Latenz.** Der Desktop-Handler triggert
   **nur** einen sofortigen Pull-Refresh (`sync_now`), wendet die Änderung
   **nicht** selbst an. Verpasste Events heilt der nächste Cycle.
3. **Durchsetzung re-nutzt ADR-0011.** Sobald `read_only` live flippt, greifen
   der bestehende Engine-Stage-Guard (`root_accepts_local_mutations`) und der
   Reconcile-Overwrite-plus-Toast automatisch: ein lokaler Edit auf dem nun
   schreibgeschützten Mount wird beim nächsten Reconcile mit der Server-Version
   überschrieben. Kein neuer Durchsetzungspfad nötig.
   Richtung `read-only → write` braucht nichts außer dem Wegfall des Guards.

### Scope-Grenze

- **Kein** Web-UI-Toggle in diesem ADR — der Endpoint ist via API/Test
  nutzbar; ein Owner-Toggle in der Web-Sicht ist Folge-Arbeit.
- **Kein** Datei-Mount-Sonderfall (Desktop ignoriert `is_file`, ADR-0011 Rev. 7
  bleibt).

## Konsequenzen

**Positiv**
- Schreibrecht-Toggle ohne Mount-Abriss — Bobs Pfadwahl/Aktivierung bleibt.
- Re-nutzt RO-Guard + Reconcile-Overwrite + Toast aus ADR-0011 vollständig;
  neuer Code ist nur Endpoint, Event, ein Pull-Diff und ein SSE-Trigger.
- Pull-autoritativ bleibt die einzige Wahrheitsquelle; SSE bleibt entbehrlich.

**Negativ / offen**
- `can_write` ist das einzige mutable Share-Feld; weitere brauchen je eine
  bewusste Erweiterung.
- Owner-Trigger fehlt ohne Web-UI eine Bedienoberfläche (Folge-Arbeit).
</content>
</invoke>
