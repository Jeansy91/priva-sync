# ADR-0080: Windows-11-Explorer-Menü über Sparse Package

**Status:** Superseded by ADR-0081
**Datum:** 2026-11-09

## Kontext

Gefordert ist das Explorer-Menü im Windows-11-Hauptmenü, passende Icons
und ein konsistentes Verhalten bei Auswahl, Daemonstatus und Sync-Roots. Der
Desktop-Client wird weiterhin als NSIS/MSI installiert; ein Umstieg auf eine
vollständige MSIX-Installation ist nicht vorgesehen.

## Entscheidung

- Ein Sparse MSIX enthält nur die Shell-DLL und die paketgebundene
  Explorer-Menüdeklaration. Das Paket ist x64; die App bleibt extern
  installiert. Unter Windows 11 (Build 22000+) registriert das
  Installationsskript dieses Paket für den aktuellen Benutzer. Unter Windows 10
  bleibt die benutzerspezifische COM-Registrierung erhalten.
- Beide Installer rufen dasselbe PowerShell-Skript auf. Fehler bei Registrierung
  oder Entfernung schlagen die Installation beziehungsweise Deinstallation
  sichtbar fehl. Das öffentliche Zertifikat wird bei Bedarf in
  `CurrentUser\TrustedPeople` importiert und vom Installer nur dann entfernt,
  wenn er es selbst hinzugefügt hat.
- Releases verwenden ein selbstsigniertes Paket-Zertifikat mit dem Publisher
  `CN=PrivaSync`. Der private PFX liegt ausschließlich als CI-Secret vor; das
  Zertifikat wird nicht ins Repository aufgenommen. CI baut und signiert das
  Sparse Package auf Linux mit `openappx`, fest auf einen Commit gepinnt.
- Das Menü erscheint nur bei einer einzelnen Auswahl innerhalb eines
  registrierten Sync-Roots und nur, wenn der Daemon Menüeinträge liefert.
  Bestehende Reihenfolge, Untermenüs und Übersetzungen bleiben erhalten.
  `SHGetStockIconInfo` liefert ein Windows-Shell-Icon je Verb.
- Außerhalb eines Sync-Roots sowie bei Mehrfachauswahl bleibt das Menü
  ausgeblendet. Damit gilt diese Regel anstelle des
  ursprünglichen ADR-0021-Verhaltens, Ordner außerhalb eines Roots zum
  Synchronisieren anzubieten.

## Konsequenzen

- Releases benötigen das Base64-kodierte PFX in
  `PRIVASYNC_APPX_SIGNING_PFX_BASE64` und sein Passwort in
  `PRIVASYNC_APPX_SIGNING_PASSWORD`. Das Zertifikat wird ausschließlich im
  TrustedPeople-Speicher des installierenden Benutzers vertraut.
- `openappx` ist eine externe Build-Abhängigkeit; der Commit-Pin macht
  reproduzierbare Builds möglich, beseitigt aber nicht den Bedarf an
  Installationstests unter Windows.
- Installieren, Aktualisieren und Entfernen müssen auf Windows 10 und Windows 11
  geprüft werden. Die Prüfung umfasst das Hauptmenü, Icons, Mehrfachauswahl,
  Pfade außerhalb von Roots und einen nicht laufenden Daemon.
