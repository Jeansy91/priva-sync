# ADR-0076: Explorer-Namespace bindet Sync-Roots als aufklappbare Ordner

**Status:** Accepted
**Datum:** 2026-11-04

## Kontext

Windows behandelt Dateisystem-Verknüpfungen als Dateien. Dadurch bleiben die
individuellen Root-Icons erhalten, die Verknüpfungen erscheinen im
Navigationsbereich aber nicht als aufklappbare Ordner. Junctions sind
aufklappbar, verlieren jedoch die individuellen Root-Icons.

## Entscheidung

Der PrivaSync-Namespace verwendet die registrierte Shell-Erweiterung als
`IShellFolder`. Sie delegiert die Anzeige und Icon-Abfrage an den
Dateisystem-Ordner mit den Root-Verknüpfungen, meldet Verknüpfungen auf
Verzeichnisse als Ordner und bindet beim Öffnen direkt an den jeweiligen
Sync-Root. Die Cloud-Files-Roots und ihre Daten bleiben an ihren bisherigen
Pfaden.

Bei mindestens zwei aktiven Sync-Roots zeigt der Navigationsbereich einen
angehefteten PrivaSync-Eintrag mit den einzelnen Roots als aufklappbare
Children. Bei weniger Roots wird der Namespace ausgeblendet und der einzelne
Cloud-Files-Root wieder angeheftet.

## Konsequenzen

- Die Explorer-Shell-DLL muss als `InProcServer32` für die Namespace-CLSID
  verfügbar sein; die App registriert den Pfad zum Verzeichnis der Root-Links.
- Root-Icons stammen weiterhin aus den jeweiligen Verknüpfungen; ihre
  `IShellFolder`-Icon-Abfrage wird an die Dateisystem-Shell weitergereicht.
- Die Verknüpfungen werden im Navigationsbereich als Ordner behandelt und
  öffnen den echten Sync-Root mit dessen Children.
- Fehlt die Shell-DLL, bleibt der bisherige Shell32-Fallback sichtbar, aber
  nicht aufklappbar; die App protokolliert diesen Fallback.
- Explorer kann die DLL bis zum Freigeben aller Shell-Objekte geladen halten;
  Upgrades müssen eine gesperrte DLL deshalb aus dem Weg verschieben können.
