# ADR-0020: Globale Sync-Ausschlussmuster (.gitignore-Semantik)

**Status:** Accepted
**Datum:** 2026-06-23

## Kontext

Nutzer wollen bestimmte Dateien von der Synchronisation ausschließen:
primär **System-Dateien** (z. B. `Thumbs.db`, `.DS_Store`) und **transiente
Datenbank-/Sperr-Dateien**, daneben gezielt einzelne Dateien aus beliebigem
Grund. Das Verhalten soll sich „ähnlich einer `.gitignore`" anfühlen, und es ist
sinnvoll, bereits eine vernünftige Liste vorzugeben.

Der Desktop-Client stagt lokale Änderungen an zwei Stellen für den Upload:
- **`reconcile_local_inner`** — der periodische Voll-Scan des Root-Verzeichnisses
  (walkdir), der neue/geänderte Dateien als `pending` und gelöschte als
  `pending_delete` einträgt;
- **`stage_upload` / `stage_delete`** (events.rs) — die Watcher-Pfade, die je ein
  Datei-Event in eine `local_files`-Zeile übersetzen.

Beide kennen bereits feste Skips (`.tmp`-Download-Tempfiles, `.conflict.`-Kopien,
dehydrierte Platzhalter). Ein nutzerdefinierter Ausschluss muss an denselben
Stellen greifen, damit weder Scan noch Watcher eine ausgeschlossene Datei stagen.

Offene Designfragen waren: **(a)** wo die Muster leben und in welchem
Gültigkeitsbereich, **(b)** in welche Richtung sie wirken, **(c)** welche
Matching-Engine, **(d)** ob Defaults vorgegeben werden.

## Entscheidung

**(a) Gültigkeit + Speicher: global, ein `config`-Key `ignore_patterns`.** Die
Muster gelten für **alle** Sync-Roots, nicht pro Root — der Haupt-Use-Case
(System-/DB-Dateien überall draußen) ist clientweit, und eine einzige zentrale
Liste in den Einstellungen ist die einfachere mentale wie technische Form.
Speicherung als roher mehrzeiliger Text im bestehenden Key-Value-`config`-Table
(kein Schema nötig); Reihenfolge, Kommentare und Negationen bleiben verlustfrei.
Gepflegt in der Desktop-Settings-UI (eine Textarea) plus Tauri-Commands
`get_ignore_patterns` / `set_ignore_patterns` / `get_default_ignore_patterns`.

**(b) Richtung: nur lokal → Server.** Eine gematchte lokale Datei wird nie für
Upload oder Delete gestagt. Server-Downloads bleiben unberührt. Das deckt den
Haupt-Use-Case (lokalen Müll nicht hochladen) ab, ohne die heiklen Edge-Cases
einer bidirektionalen Filterung (schon synchronisierte Dateien, Tombstones).

**(c) Engine: `ignore`-Crate (BurntSushi), `gitignore::Gitignore`** — exakte
gitignore-Semantik: Anchoring (`/foo`), Verzeichnis-Muster (`dir/`), Negation
(`!keep.log`), Parent-Matching. Gekapselt im schmalen Deep Module
`sync/ignore.rs` (`IgnoreMatcher`) mit der DB-Pfad-Naht `is_ignored_db_path`,
die den führenden `/` der `local_files`-Pfadform abschneidet — eine einzige
getestete Stelle, die Scan und Watcher gemeinsam nutzen.

**(d) Default-Liste in Rust als `DEFAULT_IGNORE_PATTERNS`** (OS-Müll, Temp-/
Office-Sperrdateien, transiente DB-Sidecars). Rust ist die einzige Quelle:
`get_ignore_patterns` liefert die Defaults zurück, **solange der Nutzer nie
gespeichert hat** — die sinnvollen Ausschlüsse greifen also schon auf einem
frischen Client, ohne dass jemand die Einstellungen öffnet. Die UI zeigt den Wert
in der Textarea, „Standard wiederherstellen" holt die Liste über
`get_default_ignore_patterns` zurück. Ein leer gespeicherter Wert deaktiviert
bewusst alle Ausschlüsse.

**(e) Editor mit Highlighting + Validierung.** Die Settings-Textarea ist ein
schlanker, selbstgebauter Highlight-Editor (transparente `<textarea>` über
farbigem `<pre>`, Scroll-synchron) — kein Editor-Dependency, da das Highlighting
zeilenbasiert trivial ist und Eigenbesitz das Markieren fehlerhafter Zeilen
erlaubt. **Validiert wird in Rust** über dieselbe `ignore`-Engine
(`validate_patterns` + Cmd `validate_ignore_patterns`): jede Zeile wird einzeln
kompiliert, Fehler kommen mit 1-basierter Zeilennummer zurück. So meldet der
Editor nie eine Zeile als gültig, die später still verworfen würde — keine
zweite gitignore-Implementierung in TypeScript. Fehler blockieren das Speichern.

**(f) Explorer-Status ausgeschlossener Dateien: in-sync + pinned.** Ausgeschlossene
Dateien sind reguläre Dateien im CF-Sync-Root und zeigen daher per CF-Default
„Synchronisierung ausstehend" — irreführend, da sie nie hochgeladen werden. Beim
Scan werden sie deshalb über `mark_excluded` zu **in-sync-Platzhaltern** konvertiert
(`CfConvertToPlaceholder(MARK_IN_SYNC)`) und **gepinnt** (`CfSetPinState(PINNED)`):
Explorer zeigt dann „auf diesem Gerät verfügbar" (✓), der Inhalt bleibt voll auf
Platte, und das Pinning verhindert eine Dehydrierung, die mangels Server-Kopie nie
rehydriert werden könnte. Das sind exakt die Standard-CF-APIs, die jede
synchronisierte Datei ohnehin durchläuft — kein Shell-Extension-Code. Ein eigener
„Ausgeschlossen"-Text in einer Spalte ist damit bewusst nicht das Ziel; ✓ statt
falschem „ausstehend" genügt, die wirklich differenzierte Sicht liefert die
In-App-Liste in den Einstellungen (`count_excluded_files`).

## Verworfene Alternativen

- **Pro-Root-Muster (Spalte `ignore_patterns` auf `sync_roots`).** Zuerst so
  gebaut, dann verworfen: der Bedarf ist clientweit, eine Liste pro Ordner wäre
  redundante Pflege für den Normalfall. Per-Root-Overrides bleiben eine mögliche
  spätere Ergänzung **über** der globalen Basis — dann als Folge-ADR.
- **`.privasyncignore`-Datei pro Root** (wie echte `.gitignore`, reist mit dem
  Ordner). Näher am mentalen Modell, aber die Datei wäre selbst ein
  Sync-Artefakt (mitsynchronisieren? Konflikte?) und passt nicht zur gewünschten
  zentralen Einstellung. Zurückgestellt.
- **Fest eingebaute, nicht entfernbare Default-Basis + Nutzer-Zusätze.** Sicherer
  gegen versehentliches Löschen wichtiger Skips, aber unflexibler und komplexere
  UI. Vorerst seeded-but-editable; bei Bedarf nachrüstbar.
- **Bidirektionale Filterung.** Würde serverseitige Treffer auch vom Download
  ausschließen — konsistenter, aber bringt Tombstone-/Re-Download-Logik mit. Erst
  bei belegtem Bedarf, dann Folge-ADR.
- **Eigene Explorer-Status-Spalte „Ausgeschlossen" (CF CustomStates /
  `StorageProviderItemPropertyDefinition` + `StorageProviderItemProperties::SetAsync`).**
  Zwei Versuche, beide → **Explorer-Crash** beim Öffnen des Ordners.
  Versuch 1 vermutet am `DisplayNameResource`-Literal; Versuch 2 mit korrekt in die
  `.exe` eingebetteter String-Resource (`@<exe>,-101`, via SHLoadIndirectString
  nachweislich auflösbar) crashte erneut. Event-Log-Diagnose: Fault-Modul
  **`windows.storage.dll`**, `0xc0000005` (Null-Deref) beim Verarbeiten des
  Registry-Keys `…\SyncRootManager\<id>\CustomStates\1`. Custom-Property-Spalten
  setzen faktisch ein MSIX-paketiertes Sync-Provider-Setup mit Property-Schema
  voraus; der unpackaged Win32-Provider triggert intern einen Null-Deref in
  Microsofts eigener DLL — auf unserer Seite nicht behebbar. Endgültig verworfen
  zugunsten von (f). Recovery aus dem Crash: `CustomStates`-Key je Root löschen
  (Root bleibt registriert → keine Placeholder-Orphans), Explorer neu starten.

## Konsequenzen

**Positiv:**
- Ausschlüsse greifen an genau den Staging-Stellen, an denen auch die festen
  Skips sitzen — gleiche Garantie für Scan und Watcher, kein neuer Code-Pfad.
- Sinnvolle Defaults wirken sofort auf jedem Client; eine einzige zentrale Liste
  ist leicht zu finden und zu pflegen.
- Exakte gitignore-Semantik ohne Eigenbau; `IgnoreMatcher` ist isoliert und voll
  unit-getestet (Glob, Verzeichnis, Negation, Leer/Kommentar, DB-Pfad-Naht), plus
  Integrationstests für Default-Seed und gespeicherte Muster im Reconcile.

**Negativ:**
- Neue Abhängigkeit (`ignore`-Crate) im Desktop-Build.
- Keine Pro-Root-Differenzierung (bewusst; Folge-ADR bei Bedarf).
- Downloads ignorieren die Muster: matcht eine Regel eine serverseitig schon
  vorhandene Datei, landet sie trotzdem lokal. Bewusst (siehe (b)).
- Der Watcher baut den `IgnoreMatcher` pro Event neu (ein DB-Read + Parse je
  Event, unkritisch); nur der Voll-Scan baut ihn einmal pro Durchlauf.
