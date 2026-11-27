# ADR-0075: Explorer-Namespace zeigt Sync-Roots mit ihren Ordner-Icons

**Status:** Superseded by ADR-0076
**Datum:** 2026-11-04

## Kontext

Windows registriert jeden Cloud-Files-Sync-Root einzeln im Explorer. Bei
mehreren Roots soll der Navigationsbereich einen gemeinsamen PrivaSync-Eintrag
mit den Root-Namen und ihren jeweiligen Ordner-Icons zeigen. Die Roots und
ihre Cloud-Files-Registrierungen bleiben an ihren bisherigen Pfaden.

## Entscheidung

Ab zwei aktiven Sync-Roots zeigt der Explorer einen angehefteten PrivaSync-
Namespace. Jeder Root wird darin als Verknüpfung auf seinen bestehenden
Verzeichnispfad dargestellt. Die Verknüpfung verwendet die Icon-Ressource des
Zielordners. Während der Namespace aktiv ist, werden die einzelnen
Cloud-Files-Einträge ausgeblendet.

Bei weniger als zwei Roots wird der Namespace aus dem Navigationsbereich
ausgeblendet und der einzelne Cloud-Files-Root wieder angeheftet.

## Konsequenzen

- Sync-Daten und Cloud-Files-Registrierungen bleiben an den ursprünglichen
  Pfaden; die Explorer-Einträge verweisen auf diese Pfade.
- Doppelte oder ungültige Root-Namen werden für die Verknüpfungen bereinigt
  und eindeutig gemacht.
- Windows kann Verknüpfungen mit einem Verknüpfungspfeil darstellen.
- Verknüpfungen werden im Navigationsbereich nicht als aufklappbare Ordner
  behandelt; ihre Children erscheinen dort nicht. Diese Variante priorisiert
  die individuellen Root-Icons gegenüber aufklappbaren Children.
- Kann Windows das Icon eines Roots nicht auflösen, wird der Fehler protokolliert
  und die Verknüpfung bleibt auf das Zielverzeichnis gerichtet.
