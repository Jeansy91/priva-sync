# ADR-0057: Ein Sync-Root auf einem Laufwerks-Root synchronisiert ohne Platzhalter

**Status:** Accepted
**Datum:** 2026-08-28

## Kontext

Ein Feldlauf mit `D:\` als Sync-Root zeigte eine Kette, die in einem
unbrauchbaren Zustand endet:

```
CF filter did not attach to 'D:\' within 10s
ERROR CF provider connect failed for 'Backup (D:)': CfConnectSyncRoot
ensure_placeholders: register failed: StorageProviderSyncRootManager::Register:
  Der Cloudvorgang ist ungültig. (0x8007017C)
ensure_placeholders('Backup (D:)'): 26358 files + 35680 dirs in 165712ms —
  files[succeeded=0, not_under_root=2433, failed=23925]
```

Die Cloud Filter API akzeptiert keinen Volume-Root. `Register` antwortet
`0x8007017C` (`ERROR_CLOUD_FILE_INVALID_REQUEST`), der Kernel-Filter hängt sich
nie an, `CfConnectSyncRoot` scheitert entsprechend. Der Client hat davon
unbeeindruckt weitergemacht: Registrierung halb angelegt, Provider nicht
verbunden, und anschließend 23925 Einzelkonvertierungen, die alle scheitern
mussten. Für Explorer bleibt ein Cloud-Provider für das gesamte Laufwerk
zurück, der nie antwortet — die Ursache der beobachteten Explorer-Hänger.

Der Root ist dabei fachlich völlig in Ordnung. Nichts an einer
Laufwerks-Wurzel macht sie unsynchronisierbar; nur die *Explorer-Integration*
ist dort nicht zu haben.

## Entscheidung

**Ein Root auf einem Laufwerks-Root synchronisiert vollständig, aber ohne
Cloud-Files-Platzhalter.** Das ist genau der Modus, den macOS und Linux ohnehin
fahren (ADR-Kontext: `supports_placeholders()` war bisher rein plattformweit),
also wird nicht ein zweiter Sonderweg gebaut, sondern der vorhandene
Fallback-Pfad pro Root nutzbar gemacht:

- Neu `cloud_files::placeholders_available_for(path)` =
  `supports_placeholders() && !is_volume_root(path)`. Volume-Root heißt
  schlicht `Path::parent() == None`.
- Jede CF-berührende Stelle fragt diese Funktion statt `supports_placeholders()`:
  Registrierung und `CfConnectSyncRoot` beim Start, `ensure_placeholders`,
  `hydrate_pending_placeholders`, `settle_directory_status` und der
  Download-Zweig in `reconcile`, der sonst einen Platzhalter statt der Datei
  anlegt.
- Der Download-Pfad fällt damit auf den vollständigen Download zurück — dieselbe
  Verzweigung, die Nicht-Windows-Clients seit jeher nehmen.

Was der Nutzer dadurch **nicht** bekommt: On-Demand-Hydration, das
Wolken-Symbol und die Spalte „Synchronisierungsstatus" im Explorer für diesen
Root. Was er bekommt: einen Root, der synchronisiert, statt eines Roots, der
24000 Fehler produziert und die Shell blockiert.

## Considered Options

- **Volume-Roots bei der Auswahl ablehnen.** Ehrlich und billig, aber es nimmt
  einen Anwendungsfall weg, den nichts außer der Explorer-Integration
  verhindert. Ein „Backup des ganzen Laufwerks" ist ein legitimer Wunsch.
- **Automatisch in ein Unterverzeichnis umleiten** (`D:\` → `D:\PrivaSync`).
  Löst das API-Problem, ändert aber hinter dem Rücken des Nutzers, *was*
  synchronisiert wird. Ein Pfad, den der Nutzer gewählt hat, darf nicht
  stillschweigend ein anderer werden.
- **Registrierung trotzdem versuchen und den Fehler auswerten.** Wäre
  robuster gegen künftige Windows-Änderungen als eine Pfadregel, kostet aber
  bei jedem Start den 10-Sekunden-Timeout des Filter-Probes und liefert
  dieselbe Antwort. Die Pfadregel ist die billigere Vorbedingung; scheitert die
  Registrierung aus anderen Gründen, greift weiterhin die vorhandene
  Fehlerbehandlung.

## Konsequenzen

- `D:\` funktioniert als Sync-Root, ohne Explorer-Integration und ohne die
  Fehlerlawine.
- Für solche Roots ist `download_mode` faktisch immer „eager" — On-Demand
  braucht Platzhalter. Die Einstellung bleibt sichtbar, hat dort aber keine
  Wirkung; das gehört noch in der UI kenntlich gemacht (offen).
- `record_dirty_dirs` sammelt weiterhin für jeden Root, weil es nur die Root-ID
  kennt. `settle_directory_status` leert die Sammlung deshalb jetzt *vor* der
  Platzhalter-Prüfung, sonst wüchse sie auf einem Volume-Root für die gesamte
  Prozesslaufzeit.
- Ein Root auf `D:\` zieht `$RECYCLE.BIN`, `System Volume Information` und
  Ähnliches mit. Das ist eine Frage der Ausschlussmuster (ADR-0020), nicht
  dieser Entscheidung — aber es ist der nächste Punkt, der dem Nutzer auffallen
  wird.
