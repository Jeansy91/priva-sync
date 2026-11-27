# ADR-0037: Lokaler Reconcile — selten, gechunkt, hierarchischer Diff statt Voll-Walk pro Zyklus

**Status:** Accepted
**Datum:** 2026-08-24

## Kontext

`reconcile_local` vergleicht den lokalen Dateibaum gegen den DB-Index und
staged Diskrepanzen für Upload/Delete. Aktuell läuft die Funktion
bedingungslos bei **jedem** `sync_cycle`-Aufruf — alle 30s (`POLL_INTERVAL`)
**und** bei jedem Debounce-Event vom Watcher
([engine_loop.rs:137,160-185](../../apps/desktop/src-tauri/src/sync/engine_loop.rs#L137)).
Bei Millionen-Datei-Roots skaliert das strukturell nicht:

- Zwei komplette `walkdir`-Traversierungen pro Lauf (File-Pass + Dir-Pass,
  [mod.rs:267-427](../../apps/desktop/src-tauri/src/sync/mod.rs#L267-L427)).
- `db.list_all_files(&root.id)` lädt die komplette DB-Tabelle als `Vec` in
  den RAM zur Löschungs-Erkennung ([mod.rs:429](../../apps/desktop/src-tauri/src/sync/mod.rs#L429))
  — bei Millionen Zeilen eine reale Speicherspitze pro Lauf.
- Der Watcher-Live-Pfad fängt bereits die meisten Änderungen event-basiert
  ab (ADR-0006); der Voll-Walk dupliziert diese Arbeit bei jedem Zyklus,
  obwohl sein eigentlicher Zweck nur ist, Offline-Änderungen nachzuholen.
- `walkdir` ist einzelthreadig und synchron, direkt im `async fn
  sync_cycle` aufgerufen (kein `spawn_blocking`) — blockiert die gesamte
  `select!`-Loop für die Dauer des Walks, inklusive Watcher-Events, Up-
  und Downloads derselben Root.
- Kein Fortschritts-Checkpoint: ein abgebrochener Walk (App-Crash,
  Rechner zu) beginnt beim nächsten Start komplett neu — bei
  Millionen-Dateien-Roots auf langsamem Storage (HDD: mehrere Minuten bis
  über eine Stunde) ein reales Risiko, dass ein Katch-up-Walk nie
  vollständig durchläuft.

Setzt auf [ADR-0036](0036-changes-feed-sequence-cursor.md) auf — der
paginierte Changes-Feed ist die serverseitige Voraussetzung für denselben
Skalierungsanspruch auf der Download-Seite.

## Entscheidung

Fünf Einzelentscheidungen, gemeinsam umgesetzt:

**1. Trigger-Gating statt Jeder-Zyklus.** `sync_cycle` bekommt einen
expliziten Grund (`TriggerReason::Startup | Reconnect | PeriodicSafetyNet
| WatcherEvent`), analog zum bestehenden Entscheidung/Ausführung-Muster
aus [ADR-0024](0024-desktop-engine-decision-execution-split.md). Der
Voll-Walk läuft nur bei `Startup`/`Reconnect`/`PeriodicSafetyNet`. Bei
`WatcherEvent` verarbeitet die Engine ausschließlich die vom Watcher
gemeldeten Pfade (`stage_event`) — kein Walk. `PeriodicSafetyNet`-Intervall
vorläufig **20 Minuten** (konfigurierbar; Wert ist eine Startannahme, nicht
gemessen — deckt OS-Watcher-Event-Verlust ab, z. B.
`ReadDirectoryChangesW`-Kernel-Buffer-Overlauf bei Event-Bursts).

**2. Watcher-Lücke schließen: leere Verzeichnisse.** `translate_event`
filtert `EventKind::Create` aktuell auf `is_file()`
([watcher/mod.rs:56-60](../../apps/desktop/src-tauri/src/watcher/mod.rs#L56-L60))
— ein neu angelegter leerer Ordner erzeugt kein Event. Mit Entscheidung 1
würde das bis zu 20 Minuten Sichtbarkeits-Latenz bedeuten statt heute 30s.
Neuer `FileEvent::DirCreated`-Zweig, Filter entfernt für Verzeichnisse,
`stage_event` ruft direkt `db.upsert_dir(..., "pending")`. (Verwandte,
bewusst nicht mitgelöste Lücke: `Modify(Name(To))` filtert ebenfalls auf
`is_file()` — ein Verzeichnis-Rename-Ziel bliebe unentdeckt bis zum
nächsten Voll-Walk. Für Folge-ADR/-Issue vorgemerkt.)

**3. Ein Pass statt zwei.** File-Klassifizierung und Empty-Dir-Erkennung
laufen in einer `walkdir`-Traversierung statt zwei getrennten.

**4. Chunked, checkpointed, resumable, außerhalb der Engine-Loop.** Der
Walk läuft in Chunks (Batch-Größe vorläufig 10k Einträge), mit
periodisch persistiertem Checkpoint (letzter abgeschlossener Pfad,
deterministische Reihenfolge — siehe Entscheidung 5). Ein Abbruch
(Crash, Suspend) setzt beim nächsten Start am Checkpoint fort statt bei
null. Läuft über `spawn_blocking` statt inline im `async fn sync_cycle`,
damit Watcher-Events/Uploads/Downloads derselben Root zwischen Chunks
weiterlaufen. Ein Overlap-Guard pro Root (In-Progress-Flag) verhindert,
dass zwei Walk-Ketten für dieselbe Root gleichzeitig laufen (z. B.
Safety-Net-Trigger während ein Start-Walk noch läuft).

**5. Löschungs-Erkennung: hierarchischer Lockstep-Merge statt Voll-Vec.**
Statt `db.list_all_files` komplett zu laden und gegen ein `HashSet` aus
dem Walk zu diffen: pro Verzeichnis wird lokal gewalkt UND die DB
verzeichnis-skopiert abgefragt (`path >= 'dir/' AND path < 'dir0'`,
Präfix-Trick), beide Seiten desselben Verzeichnisses verglichen, dann
rekursiert — Speicherbedarf gebunden auf Tiefe × Verzeichnisbreite statt
auf die Root-Größe. Sortierschlüssel: Verzeichnis-Namen werden für den
Vergleich als `name + "/"` behandelt (Git-Tree-Sort-Konvention), Dateien
roh — das reproduziert exakt die Byte-weise Sortierung, die SQLites
Standard-`BINARY`-Kollation für `ORDER BY path` liefert. Ein globaler
Sort-Merge über den ganzen Baum wurde verworfen (siehe Alternativen).

**Bedingungen, Teil der Entscheidung:**
- **NFC-Normalisierung als projektweite Invariante** an der Walk/DB-Grenze
  — macOS/APFS liefert NFD-zerlegte Namen von `readdir`, SQLite muss
  konsistent NFC speichern, sonst divergiert der Byte-Vergleich und
  erzeugt falsche „gelöscht"-Treffer.
- **Unit-Tests für Interleaving-Grenzfälle** (`foo` vs. `foo-bar.txt` vs.
  `foo/child.txt`) — genau die Fälle, in denen naive String-Sortierung
  ohne Trailing-Slash-Trick divergiert.
- **Re-Stat unmittelbar vor jeder Lösch-Aktion** als Verteidigungslinie —
  ein Sortier-/Normalisierungs-Bug in dieser Diff-Logik bedeutet sonst
  stillen Datenverlust statt einer falschen Logzeile.

## Considered Options

- **Globaler Sort-Merge über den ganzen Baum** (ein `walkdir`-Durchlauf
  mit explizit sortierten Geschwistern gegen eine sortiert paginierte
  `SELECT ... ORDER BY path`-Abfrage) — funktioniert nachweislich korrekt
  mit demselben Trailing-Slash-Trick, aber verlangt einen globalen
  Ordnungs-Äquivalenz-Beweis über den ganzen Baum. Verworfen zugunsten des
  hierarchischen Lockstep-Merges: gleiche Speichergarantie, aber die
  Korrektheit ist pro Verzeichnis statt global zu beweisen — einfacher zu
  testen, gleiche Bounded-Memory-Eigenschaft.
- **Voll-Vec-Load, nur Spalten-Projektion statt ganzer Rows** — einfacher,
  aber immer noch O(Root-Größe) Speicher statt O(Tiefe × Breite). Als
  Zwischenschritt erwogen, aber der hierarchische Merge kostet keinen
  spürbaren Mehraufwand gegenüber der ohnehin geplanten Chunking-Struktur
  (Entscheidung 4) und ist die stärkere Garantie.
- **Verzeichnis-mtime-Pruning** (unveränderte Unterbäume am Verzeichnis-
  mtime überspringen) — verworfen: mtime-Semantik variiert je Dateisystem
  und bei Cloud-Files-Platzhaltern, ungemessener Bedarf, da die
  Frequenz-Reduktion (Entscheidung 1) das ursprüngliche Skalierungs-
  problem bereits strukturell löst.

## Konsequenzen

**Positiv:**
- Voll-Walk läuft nur noch selten (Start/Reconnect/alle 20min) statt alle
  30s — Haupt-Skalierungsproblem strukturell gelöst.
- Speicherbedarf der Löschungs-Erkennung skaliert mit Baumtiefe×Breite,
  nicht mit Root-Größe.
- Engine-Loop bleibt während eines langen Walks responsiv für dieselbe
  Root (Uploads/Downloads/Watcher-Events laufen weiter).
- Abgebrochene Walks verlieren keinen Fortschritt mehr.
- Leere Ordner werden wieder in Echtzeit sichtbar (Watcher-Event statt
  20-Minuten-Warten aufs Safety-Net).

**Negativ:**
- Deutlich mehr bewegliche Teile als die bisherige einfache
  Zwei-Pass-Funktion: `TriggerReason`-Enum, Chunk/Checkpoint-Persistenz,
  Overlap-Guard, verzeichnis-skopierte DB-Queries, NFC-Normalisierungs-
  Invariante — jedes Teil eigenständig zu testen.
- NFC-Normalisierung muss konsequent an jeder Stelle durchgesetzt werden,
  die Pfade schreibt oder liest (Walk, DB-Insert, API-Client) — ein
  vergessener Pfad unterläuft die Korrektheitsgarantie der ganzen
  Diff-Logik.
- 20-Minuten-Safety-Net-Intervall und 10k-Chunk-Größe sind unkalibrierte
  Startwerte — Messung im Betrieb aussteht (analog zum offenen Punkt in
  ADR-0002 zum Block-Hash-Cache).
- `Modify(Name(To))`-Filter auf Verzeichnisse bleibt eine offene, bewusst
  nicht mitgelöste Lücke (Entscheidung 2).
