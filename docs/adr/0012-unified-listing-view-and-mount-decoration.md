# ADR-0012: Vereinheitlichte Listing-View und Mount-Decoration

**Status:** Proposed
**Datum:** 2026-05-28

## Kontext

Die Web-Files-Oberfläche pflegt derzeit drei nahezu redundante View-Komponenten:

- [`FilesTableView`](../../apps/web/src/pages/files/FilesTableView.tsx) — flacher Listing innerhalb eines Sync-Roots.
- [`FilesGridView`](../../apps/web/src/pages/files/FilesGridView.tsx) — Grid-Variante davon.
- [`FilesGlobalSearchResults`](../../apps/web/src/pages/files/FilesGlobalSearchResults.tsx) — gruppierte Tabelle pro Treffer-Root.
- [`FilesRootSectionsView`](../../apps/web/src/pages/files/FilesRootSectionsView.tsx) — gruppierte Tabelle für die Root-Liste („Eigene" + „Mit mir geteilt").

Hinzu kommt im Datenmodell ein dritter Eintrags-Kind: `DetailSelection.kind: 'file' | 'dir' | 'mount'`. Ein Shared Mount ist damit ein eigener Typ neben Datei und Verzeichnis — obwohl er sich, sobald man ihn betritt, exakt wie ein Sync-Root verhält und ein Datei-Mount sich exakt wie eine Datei verhält. ADR-0010 hatte die Empfänger-Sicht skizziert, das UI-Modell aber nicht ausformuliert.

Folgen des Status-Quo:

- Drei Code-Pfade rendern „Zeile mit Icon, Name, Aktion-Spalte"; jede Änderung (z.B. ShareBadge, Multiselect, Lasso, Doppelklick-Logik) muss an drei Stellen nachgezogen werden — oder driftet auseinander.
- Mount-Spezifika (Geber-Pille, `can_write`, `expires_at`, Verlassen-Aktion) wurden mehrfach als ad-hoc-Bedingungen über die Komponenten gestreut.
- Detail-Panel hat eine eigene `MountBody`-Variante, deren Layout willkürlich von `FileBody`/`DirBody` abweicht.
- File-Mounts bekamen anfangs ein generisches File-Icon ohne Thumb; Aktion-Verfügbarkeit (Download/Vorschau) hing am Mount-Kind statt am tatsächlichen Inhalt.

## Entscheidung

### Mount-ness wird Decoration, kein eigener Kind

`DirEntry` und `DetailSelection` behalten ihre zwei Kinds (`file`, `dir`) und tragen ein optionales `mount?: SharedMount`. Die `'mount'`-Variante entfällt.

```ts
type DirEntry =
  | { kind: 'dir';  name: string; path: string;        mount?: SharedMount }
  | { kind: 'file'; name: string; file: FileEntry;     mount?: SharedMount }

type DetailSelection =
  | { kind: 'file'; file: FileEntry; mount?: SharedMount }
  | { kind: 'dir';  /* aggregate fields */; mount?: SharedMount }
```

Konsequenzen für die Datenflüsse:

- **File-Mount** = `kind: 'file'` mit `mount` gesetzt; alle bestehenden File-Code-Pfade greifen.
- **Dir-Mount** = `kind: 'dir'` mit `mount` gesetzt; alle bestehenden Dir-Code-Pfade greifen.
- **Innerhalb eines Mounts** trägt jeder Eintrag dasselbe `mount`-Objekt weiter (Mount-Kontext). Dispatch zwischen Sync-Root- und Mount-Endpoints geht künftig über `entry.mount`, nicht über das alte ad-hoc `mountMode`-Flag.

### Eine Listing-Komponente mit optionalen Sektionen

Wir kollabieren `FilesTableView`, `FilesGridView`, `FilesGlobalSearchResults` und `FilesRootSectionsView` in eine `FilesListing`-Komponente:

```ts
type Section = { header?: string; entries: DirEntry[] }
type Props = {
  sections: Section[]
  viewMode: 'table' | 'grid'
  ...
}
```

- Eine Sektion ohne `header` → flache Ansicht (Subdir-Listing).
- Mehrere Sektionen mit `header` → collapsible Header zwischen tbody-Gruppen, sticky-Verhalten wie im heutigen Global-Search.
- Sortierung gilt **pro Sektion** (Header-Reihen sind nur visuell, kein Eintrag).
- Multiselect/Lasso ignorieren Sektions-Header (Hit-Test schließt sie aus).

### Detail-Panel bleibt prop-getrieben

`FileDetailPanel` rendert Daten anhand der `DetailSelection`-Properties; die `MountBody`-Variante entfällt. Mount-Felder (Geber, Zugriff, Ablauf) erscheinen als zusätzliche Reihen, wenn `selection.mount` gesetzt ist — *unterhalb* der gemeinsamen Daten-Reihen. Layout für eigene Subdir und Dir-Mount ist damit Pixel-identisch bis auf das Mount-Anhängsel.

Aktion-Surface ist auf das nötige Minimum reduziert:

| Eintrag | Detail-Buttons | Doppelklick + Kontextmenü |
|---|---|---|
| Datei (eigen) | Herunterladen, Teilen | Vorschau, Löschen, … |
| Verzeichnis (eigen, inkl. Sync-Root) | Herunterladen (ZIP), Teilen | Öffnen, Löschen |
| File-Mount | Herunterladen | Vorschau, Verlassen |
| Dir-Mount | Herunterladen (ZIP) | Öffnen, Verlassen |

„Vorschau", „Verlassen", „Öffnen", „Löschen" sind **nicht** im Detail-Panel — sie haben einen sichtbareren Surface (Doppelklick / Kontextmenü).

### Server-Contract: lazy Stats + Dir-Mount-Download

Zwei neue Endpoints, beide auf der bestehenden Mount-Permission-Logik:

- `GET /api/v1/shared-mounts/{id}/stats` — liefert je nach `is_file` entweder Datei-Meta (`size`, `mtime`, `content_hash`, `updated_at`, `id`) oder Verzeichnis-Aggregat (`item_count`, `total_size`, `latest_mtime`). Wird vom Detail-Panel auf Klick gefetcht; in der Listing-Page selbst werden **keine** Aggregate vorgeladen (vermeidet N Subtree-Scans pro Liste).
- `POST /api/v1/shared-mounts/{id}/download-token` — mintet einen Token nach dem bestehenden `downloadGrant`-Schema mit `UserID=share.UserID`, `RootID=*share.SyncRootID`, `Path=share.Path`. Reuse der `streamSyncRootZip`-Logik. File-Mount-Downloads laufen weiter über `GET /shared-mounts/{id}/download?path=/` (kein ZIP-Umweg).

Frontend-Wrapper `apiClient.sharedMounts.downloadAction(m)` dispatcht über `m.is_file`.

### Stats-Quelle dispatcht in `FilesPage`, nicht im Panel

Der bestehende `useEffect`, der `directoryStats` für die Detail-Auswahl fetcht, bekommt einen Branch:

```ts
if (selection.mount) apiClient.sharedMounts.stats(selection.mount.id)
else                 apiClient.files.directoryStats(rootID, effectiveDirPath)
```

`FileDetailPanel` bleibt API-frei und damit testbar mit Stub-Stats.

## Konsequenzen

### Positiv

- Ein Code-Pfad pro „Zeile zeichnen" / „Detail rendern". Künftige Änderungen (z.B. Spalten, Drag-Drop, Inline-Aktionen) leben an einer Stelle.
- Sync-Root, Subdir und Dir-Mount in der Root-Liste werden visuell und interaktiv identisch — Lernkurve sinkt.
- Mount-Spezifika sind explizit Decoration: `entry.mount` ist der einzige Marker, kein zweiter Discriminator über mehrere Layer.
- Backend-Kosten bleiben kontrolliert: keine Pre-Aggregation in der Mounts-Liste.

### Negativ / offen

- Der Refactor berührt Multiselect/Lasso/Sort/Section-Render an einer Stelle gleichzeitig — entsprechend großer Vertical-Slice. Aufteilung in mehrere TDD-Cycles bleibt möglich, indem die alten Komponenten zunächst auf `FilesListing` umgeleitet werden, bevor sie entfernt werden.
- `mountMode`-Logik in `FilesPage` ist mit der Decoration redundant; wird aufgeräumt, sobald `entry.mount` durchgängig fließt — eigener Cleanup-Slice.
- `FilesGlobalSearchResults` hat heute Mount-Doppelklick-Pfade (`onActivateMountFile`/`onActivateMountDir`), die in den gemeinsamen `onActivate(entry)` aufgehen müssen.
- ADR-0011 (Desktop-Sync) ist nicht betroffen — das Decoration-Modell ist eine Web-Frontend-Entscheidung. Desktop teilt sich nur die API-Typen.

### Folge-ADRs

- ADR-0013 löst das Layout-Versprechen „Letzte Änderung von" ein; ohne ADR-0013 bleibt diese Reihe ungerendert.
- Wenn der `last_modifier`-Datenpunkt ankommt, erscheint er als zusätzliche Reihe in beiden Pfaden — Layout bleibt unverändert.
