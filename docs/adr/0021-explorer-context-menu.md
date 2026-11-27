# ADR-0021: Explorer-Kontextmenü über Shell-Ext + Named-Pipe + Daemon-Service

**Status:** Superseded by ADR-0080 (Windows registration and menu scope)
**Datum:** 2026-06-25

## Kontext

Nutzer wollen Sync-/Share-Aktionen direkt aus dem Datei-Explorer auslösen
: an Dateien/Ordnern **innerhalb** eines Sync-Roots `Teilen`
(Untermenü: mit Benutzer teilen / öffentlichen Link erstellen+kopieren),
`Link kopieren`, `Im Web öffnen`, `Von Synchronisation ausschließen`,
`Freigaben verwalten`, `Konflikt auflösen`, `Pin/Unpin`; an Ordnern
**außerhalb** `Synchronisieren`. Die sichtbaren Punkte hängen vom Zustand des
Pfades ab (geteilt? ausgeschlossen? gepinnt? im Konflikt?).

Randbedingungen aus dem Bestand:

- Ein Explorer-Kontextmenü-Handler läuft **in-proc in `explorer.exe`** — er
  kann den laufenden Tauri-Daemon (eigener Prozess) nicht direkt aufrufen.
- Es gibt **keinen** lokalen Steuerkanal und **kein** Deep-Link-Plugin.
- Der Build ist **NSIS/MSI ohne Paket-Identität** (`bundle.targets = "all"`),
  also **kein MSIX** — moderne, paket-gebundene Verb-Deklarationen scheiden aus.
- Lehre aus ADR-0020 (f): Der Versuch, den Cloud-Files-Provider um
  **CustomStates / `StorageProviderItemPropertySource`** zu erweitern,
  crasht Explorer (`windows.storage.dll`, Null-Deref) bei einem **unpaketierten**
  Win32-Provider. Alles am `SyncRootManager`-Erweiterungsweg ist verbrannt.
- ADR-0005 kündigt eine `privasync_shell.dll` (damals für `IColumnProvider`)
  bereits als künftige Shell-Extension an.

## Entscheidung

Dreischichtig: **dünne Shell-Ext** (Darstellung) → **Named Pipe** (Transport)
→ **`ContextMenuService` im Daemon** (Logik, Single Source of Truth).

```
explorer.exe
  └─ privasync-shell (COM cdylib: IExplorerCommand + IEnumExplorerCommand)
        │  {"op":"state","path":…}  →  {in_root,shared,excluded,pinned,conflict,…}
        │  {"op":"invoke","verb":…,"path":…}  →  ok/err
        ▼  Named Pipe  \\.\pipe\privasync   (NDJSON, ein Request/Response je Zeile)
  privasync-desktop (laufender Daemon)
        └─ contextmenu::ContextMenuService     ← Deep Module, hier alle Tests
             ├─ state(path)  -> PathMenuState   (Pfad-Klassifikation gegen DB)
             ├─ items(state) -> Vec<MenuVerb>   (reine, zustandsgegatete Tabelle)
             └─ invoke(verb, path)              (dispatch auf bestehende Commands)
```

**(a) Mechanismus: klassische in-proc COM-Shell-Extension.** Eigenes cdylib-Crate
`privasync-shell` implementiert `IExplorerCommand`; das `Teilen`-Untermenü über
`IExplorerCommand::EnumSubCommands` (`IEnumExplorerCommand`). Registrierung
klassisch per Registry unter `HKCU\Software\Classes\CLSID\{guid}` +
Verknüpfung an die Dateiklasse / den Sync-Root — **nicht** über
`SyncRootManager`-CustomStates (siehe ADR-0020 (f): Crash). Win11 versteckt
Legacy-`IContextMenu` hinter „Weitere Optionen anzeigen"; `IExplorerCommand`
erscheint dagegen im Hauptmenü.

**(b) Logik komplett im Daemon, Shell-Ext bleibt dumm.** Die Extension hält
**keine** Geschäftslogik: sie fragt `state(path)`, zeichnet daraus das Menü und
meldet `invoke(verb, path)` zurück. Damit lebt die gesamte testbare Logik in
einem Prozess mit DB- und Cloud-Files-Zugriff, und die Verb-Tabelle hat genau
**eine** Quelle.

**(c) `ContextMenuService` als Deep Module.**
- `state(path) -> PathMenuState`: klassifiziert den Pfad gegen die lokale DB
  (liegt er in einem Sync-Root? geteilt? durch Ausschlussmuster ausgeschlossen?
  gepinnt/dehydriert? Konflikt-Kopie vorhanden?).
- `items(state) -> Vec<MenuVerb>`: **reine** Funktion, die aus dem Zustand die
  sichtbaren Verben (inkl. Toggle-Beschriftung) ableitet — das Pendant zu
  `buildEntryMenuItems` der Web-UI. Hierauf zielt die Haupt-Testmasse.
- `invoke(verb, path)`: mappt jedes Verb auf **bestehende** Bausteine —
  `force_sync`, Share-Dialog/Grants (ADR-0015), Web-Deep-Link, hydrate/dehydrate
  (CF Pin-State), Konfliktauflösung (ADR-0016), Exclude-Toggle (siehe (e)).

**(d) Transport: Named Pipe `\\.\pipe\privasync`, NDJSON.** Windows-nativ, kein
TCP-Port, ACL auf den aktuellen Nutzer. Zwei Operationen (`state`, `invoke`), je
ein JSON-Objekt pro Zeile. Der Daemon betreibt einen schlanken Pipe-Server; die
Shell-Ext ist Client. `state` muss schnell antworten (Explorer ruft `GetState`
synchron) — ein lokaler Pipe-Roundtrip liegt klar darunter.

**(e) `Von Synchronisation ausschließen` brückt zu den Ausschlussmustern (ADR-0020) pro Pfad.**
ADR-0020 speichert **globale** gitignore-Muster. Das Verb hängt ein **pfad-verankertes
Muster** (relativ zum Root, mit führendem `/`) an die globale Liste an; das
Gegen-Verb `Wieder einschließen` entfernt genau dieses Muster. `items()` wählt
die Beschriftung danach, ob das Muster bereits vorhanden ist. Keine zweite
Ausschluss-Mechanik — dieselbe `IgnoreMatcher`-Naht greift beim Staging.

**(f) Plattform-Strategie: Kern portabel, Hülle + Transport per `cfg`.**
`ContextMenuService` (state/items/invoke, Verb-Enum) ist OS-unabhängig. Nur die
Front-Schicht ist plattformspezifisch und wird **zuerst nur für Windows**
gebaut: **macOS** → Finder Sync Extension (`menu(for:)`, App-Extension im
`.app`) über Unix-Domain-Socket/XPC; **Linux** → kein Standard, pro Dateimanager
(Nautilus-Extension, KDE-ServiceMenu, Nemo/Caja-Actions), ebenfalls
Unix-Domain-Socket. Beide sind Folge-Issues und erfordern **kein** Re-Design des
Kerns.

**(g) Crate-Struktur: `apps/desktop/shell` als eigenständiges cdylib-Crate.**
Zunächst als separates Crate gehalten (nicht als Cargo-Workspace-Member),
damit der bereits verifizierte Tauri-Daemon-Build unangetastet bleibt; eine
spätere Zusammenführung zu einem Workspace ist möglich. Die Shell-Ext hängt
**nicht** an Daemon-Interna — der Vertrag ist allein das NDJSON-Pipe-Protokoll
(in `shell/src/proto.rs` gespiegelt, Verben als Strings über die Naht), wodurch
die Kopplung minimal bleibt.

## Verworfene Alternativen

- **CF-`SyncRootManager`-Erweiterung (CustomStates / Command-Source).** Der
  „idiomatische" Cloud-Provider-Weg, aber für unpaketierte Win32-Provider
  crasht Explorer bereits bei CustomStates (ADR-0020 (f)). Nicht riskierbar.
- **Sparse-MSIX-Paket nur für die Verb-Deklaration.** Würde moderne
  `IExplorerCommand`-Registrierung über Paket-Identität erlauben, zwingt aber den
  ganzen Client unter MSIX-Verpackung/Signierung — großer Eingriff für ein
  Feature. Klassische COM-Registrierung erreicht dasselbe ohne Identität.
- **Lokales HTTP-Loopback statt Pipe.** Funktioniert, öffnet aber einen
  TCP-Port und braucht ein Auth-Token gegen andere lokale Prozesse. Die Pipe ist
  per-User-ACL-geschützt und portfrei — sauberer für ein Windows-only-Feature.
- **Shell-Ext liest die SQLite-DB des Daemons direkt** (State ohne IPC). Koppelt
  die Extension ans DB-Schema und riskiert Lock-Contention im Explorer-Prozess;
  `invoke` bräuchte ohnehin einen Kanal. Verworfen zugunsten einer einzigen Naht.
- **Pro Klick die App per Deep-Link starten** (kein State-Query). Dann ließen
  sich Verben nicht zustandsabhängig ein-/ausblenden, und ein Kaltstart je
  Rechtsklick ist zu träge für `GetState`. Pipe an den laufenden Daemon gewinnt.
- **Native Verben nachbauen** (Öffnen/Umbenennen/Kopieren/Verschieben/Löschen).
  Kann Explorer selbst; das eigene Menü enthält ausschließlich
  server-/sync-spezifische Aktionen, genestet unter einem Eintrag.

## Konsequenzen

**Positiv:**
- Gesamte Logik in einem testbaren Deep Module (`contextmenu`), `items()` als
  reine Tabelle unit-getestet wie `buildEntryMenuItems`; Shell-Ext und Pipe sind
  dünne, manuell verifizierte Glue-Schicht.
- Eine einzige Verb-Quelle, eine einzige Exclude-Mechanik (Wiederverwendung von
  ADR-0020 statt Parallelpfad).
- Vermeidet den bekannten CF-CustomStates-Crash und braucht keine MSIX-Identität.
- Kern ist portabel: Mac/Linux brauchen nur neue Hüllen, kein Re-Design.

**Negativ:**
- Neues cdylib-Crate + COM-Boilerplate (`IExplorerCommand`/`IEnumExplorerCommand`)
  und Registry-Registrierung/-Deregistrierung im Installer/Lifecycle.
- Neuer Named-Pipe-Server im Daemon (Lebenszyklus, Fehlerfälle, wenn der Daemon
  nicht läuft → Extension muss leeres Menü liefern, nicht blockieren).
- Windows zuerst; Mac/Linux-Parität ist Folgearbeit (bewusst, siehe (f)).
- Per-Pfad-Ausschluss erweitert die globale Ausschlussliste um pfad-verankerte
  Einträge — die Settings-Textarea zeigt diese künftig gemischt mit den
  generischen Mustern (akzeptiert; bei Bedarf spätere UI-Gruppierung).
