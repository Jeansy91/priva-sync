# ADR-0074: Mehrere Sync-Roots erscheinen unter einem PrivaSync-Namespace

**Status:** Superseded by ADR-0075

**Datum:** 2026-10-23

## Kontext

Windows registriert jeden Cloud-Files-Sync-Root einzeln im Explorer. Bei mehreren
PrivaSync-Roots erscheinen dadurch viele Einträge im Navigationsbereich. Die
lokalen Root-Verzeichnisse sollen unabhängig voneinander an ihren bisherigen
Orten bleiben.

## Entscheidung

Ab zwei aktiven Sync-Roots zeigt der Explorer einen angehefteten Namespace
**PrivaSync** mit den Roots als untergeordneten Ordnern.

- Der Namespace verweist auf ein benutzerspezifisches Verzeichnis außerhalb
  aller Sync-Roots. Darin liegt je Root eine Verzeichnis-Junction auf den
  unveränderten lokalen Pfad.
- Die Cloud-Files-Registrierungen behalten ihre ursprünglichen lokalen Pfade.
  Ihre einzelnen Explorer-Einträge werden ausgeblendet, solange die Gruppierung
  aktiv ist.
- Bei weniger als zwei Roots wird der Namespace aus dem Navigationsbereich
  ausgeblendet und der einzelne Root wieder direkt angeheftet.
- Verwaiste PrivaSync-Registry-Einträge werden entfernt, wenn ihr Pfad nicht
  mehr aktiv und das Verzeichnis nicht mehr vorhanden ist. Bestehende
  Verzeichnisse werden nicht automatisch aus der Registrierung entfernt.

## Konsequenzen

- Sync-Roots lassen sich im Explorer gesammelt öffnen, ohne sie in ein
  gemeinsames Datenverzeichnis zu verschieben.
- Windows Cloud Files erkennt die Roots weiterhin an ihren ursprünglichen
  Pfaden.
- Der Namespace ist benutzerspezifisch und wird beim Aktualisieren der
  Sync-Roots mit den aktiven Einträgen abgeglichen.
