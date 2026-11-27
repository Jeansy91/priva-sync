# ADR-0082: Explorer-Menü-Icons aus dem Web-Iconset und flaches Untermenü

**Status:** Accepted
**Datum:** 2026-11-10

## Kontext

Die Windows-Stock-Icons (`SHGetStockIconInfo`) wirkten uneinheitlich und
passten nicht zum PrivaSync-Auftritt. Das Windows-11-Hauptmenü unterstützt
außerdem nur eine Untermenü-Ebene; die verschachtelte Gruppe „Teilen" erschien
dort ohne Chevron und war nicht bedienbar.

## Entscheidung

- Der Root-Eintrag zeigt das PrivaSync-Logo, alle Verben zeigen Icons aus
  Lucide (dasselbe Iconset wie das Web-Frontend). Die SVG-Quellen und die
  daraus gerenderten `.ico`-Dateien liegen unter
  `apps/desktop/src-tauri/icons/menu/`.
- Die Icons werden als Bundle-Ressource `menu-icons/` neben der Shell-DLL
  installiert; `GetIcon` löst den Pfad relativ zur DLL auf.
- Das Shell-Modul hebt verschachtelte Container-Knoten beim Aufbau der
  `IExplorerCommand`-Liste in die Ebene des Elternknotens. Der Daemon-Menübaum
  bleibt unverändert.

## Konsequenzen

- Ändert sich ein Verb-Icon, müssen SVG und `.ico` gemeinsam aktualisiert
  werden.
- Es gibt keine Gruppenüberschrift „Teilen" mehr im Win11-Menü; die Labels der
  Einträge sind eigenständig verständlich.
