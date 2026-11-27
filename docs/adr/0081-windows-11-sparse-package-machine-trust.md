# ADR-0081: Maschinenweiter Trust für das Windows-11-Sparse-Package

**Status:** Accepted
**Datum:** 2026-11-09

## Kontext

Windows akzeptiert das selbstsignierte Sparse MSIX in der unterstützten
Installationskonfiguration nicht, wenn sein Signierzertifikat nur in
`CurrentUser\TrustedPeople` liegt. Die Installation scheitert mit
`0x800B0109` (untrusted root). Für selbstsignierte Pakete verlangt Windows den
Speicher `LocalMachine\TrustedPeople`; dieser Vertrauensanker gilt für alle
Benutzer des Rechners und erfordert Administratorrechte.

## Entscheidung

- Der Installer importiert das öffentliche Zertifikat bei Bedarf nach
  `LocalMachine\TrustedPeople`. Die Installation fordert dafür über UAC
  Administratorrechte an. Wird die UAC-Freigabe abgelehnt, wird die App ohne
  das Windows-11-Explorer-Menü installiert; andere Registrierungsfehler
  schlagen weiterhin sichtbar fehl.
- Vor dem Import vergleicht der Installer den Signierer des Sparse MSIX mit
  dem mitgelieferten öffentlichen Zertifikat.
- Der Installer vermerkt maschinenweit nur Zertifikate, die er selbst
  hinzugefügt hat. Bei der Deinstallation werden diese erst entfernt, wenn
  kein Benutzer mehr das PrivaSync-Sparse-Package registriert hat.
- Bereits vorhandene, nicht vom Installer hinzugefügte Zertifikate bleiben
  unangetastet. Upgrades entfernen den Trust-Anker nicht.

## Konsequenzen

- Die Installation und die vollständige Bereinigung können UAC erfordern.
  Das Zertifikat ist systemweit vertrauenswürdig, solange mindestens ein
  Benutzer das Paket verwendet.
- Ohne UAC-Freigabe fehlt das Windows-11-Explorer-Menü, die App bleibt aber
  nutzbar. Die Installer melden diese Einschränkung als Warnung.
- Ein Zertifikat, das vor einem fehlgeschlagenen Paket-Deployment hinzugefügt
  wurde, wird entfernt, sofern keine andere Benutzerinstallation davon abhängt.
- Wird die Zertifikatsbereinigung bei der Deinstallation abgelehnt, wird die
  App trotzdem entfernt; der maschinenweite Trust-Eintrag bleibt bestehen.
