# ADR-0077: Uploads lesen aus einem Snapshot, der unter Schreibsperre entsteht

**Status:** Superseded by ADR-0078
**Datum:** 2026-11-05

## Kontext

Manche Programme schreiben in place in eine Datei, solange sie geöffnet ist:
Access/Jet (`.accdb`, `.mdb`), SQLite, viele eingebettete Anwendungsdatenbanken.
Der Desktop-Client las solche Dateien beim Upload mit dem Standard-Share-Modus
(`FILE_SHARE_READ | WRITE | DELETE`). Ein gleichzeitiger Schreibvorgang landete
mitten in der Kopie. Auf dem Server kam eine Datei an, die gültig aussieht, aber
kaputt sein kann.

Drei Lücken trugen dazu bei:

- Die Lock-Dateien `.laccdb`/`.ldb` sind ausgeschlossen (ADR-0020). Die
  Datenbank daneben wurde trotzdem hochgeladen, auch während sie geöffnet war.
- Die Ruheprüfung vergleicht Größe und mtime. In-place-Schreiber ändern Seiten,
  ohne die Größe zu ändern, und Windows aktualisiert mtime nicht verlässlich,
  solange der Handle offen ist.
- Der Hash entstand in `process_uploads`, die Bytes las der Upload danach
  erneut. Hash und Inhalt konnten aus zwei verschiedenen Ständen stammen.

## Entscheidung

**Jeder Datei-Upload liest aus einem Snapshot. Der Snapshot entsteht unter einer
Schreibsperre und wird im selben Durchgang gehasht.**

1. **Lock-Datei vor dem Öffnen.** Gibt es neben einer Jet/ACE-Datei (`.accdb`,
   `.accde`, `.accdr`, `.accdt`, `.mdb`, `.mde`, `.mda`) eine
   `<Name>.laccdb` oder `<Name>.ldb`, ist die Datenbank geöffnet. Der Upload
   wird zurückgestellt, ohne die Datei zu öffnen. Das deckt auch den Fall ab,
   dass Access gerade nicht schreibt, aber mitten in einer Transaktion steht.
2. **Schreibsperre beim Lesen (Windows).** Die Quelle wird mit
   `FILE_SHARE_READ | FILE_SHARE_DELETE` geöffnet, also ohne `FILE_SHARE_WRITE`.
   - Hält ein anderer Prozess einen Handle mit Schreibzugriff, scheitert das
     Öffnen mit `ERROR_SHARING_VIOLATION`. Das gilt als „in Benutzung“.
     Dasselbe gilt für `ERROR_LOCK_VIOLATION` beim Lesen gesperrter
     Byte-Bereiche.
   - `FILE_SHARE_DELETE` bleibt erlaubt, damit Programme, die per
     Temp-Datei und Rename speichern, während des Kopierens nicht scheitern.
   - Die Regel gilt für alle Dateien, nicht nur für eine Liste bekannter
     Typen: Jeder unbekannte In-place-Schreiber wäre sonst wieder eine stille
     Korruption.
3. **Kurze Sperre, ein Durchgang.** Die gesperrte Quelle wird in einen
   Snapshot `<Name>.<uuid>.tmp` daneben kopiert (selbes Namensschema wie
   ADR-0047, Walk und Watcher überspringen `.tmp`). Dabei wird der BLAKE3-Hash
   berechnet. Danach wird der Handle sofort geschlossen.
   - Voll-, Chunked- und Delta-Upload lesen nur aus dem Snapshot.
   - Dem Server werden Hash, Größe und mtime aus genau diesem Snapshot
     genannt.
   - Der Snapshot wird gelöscht, sobald der Upload-Task endet.
4. **Status `in_use`.** Eine zurückgestellte Datei bekommt den Status `in_use`.
   Das Muster ist dasselbe wie bei `blocked_remote_quota` (ADR-0011):
   - Ein neuer Versuch läuft fest alle 60 s, ohne Backoff, und
     `attempt_count` wächst nicht. Gewartet wird auf den Nutzer, nicht auf
     einen Fehler.
   - Die Datei erscheint unter „Sync-Probleme“ mit dem Hinweis „Datei ist
     geöffnet“.
   - Sie zählt nicht in `list_pending_all`, damit der Tray nicht dauerhaft auf
     „Synchronisiert…“ steht.
   - `process_uploads` prüft die Sperre schon vor dem Hashen. So wird eine
     geöffnete Datenbank nicht bei jedem Zyklus komplett gelesen.

## Konsequenzen

- Geöffnete Datenbanken werden erst nach dem Schließen hochgeladen. Für
  Datenbanken, die praktisch nie geschlossen werden, braucht es eine Sicherung
  auf Anwendungsebene, z. B. eine nächtliche Kopie in den Sync-Ordner, während
  die Live-Datei per Ausschlussmuster ausgenommen ist.
- Hält ein Programm eine geöffnete Datei mit Schreibzugriff offen, obwohl es
  per Rename speichert, wird ebenfalls erst nach dem Schließen hochgeladen.
  Das kostet Zeit, keine Daten. Für Office ist das nicht verifiziert.
- Jeder Upload kostet einen zusätzlichen lokalen Kopiervorgang und kurzzeitig
  Speicherplatz bis zur Dateigröße auf dem Volume der Datei. Das ist der Preis
  dafür, dass Hash und Bytes garantiert zusammenpassen und die Quelle nur für
  die Dauer der lokalen Kopie gesperrt ist.
- Außerhalb von Windows gibt es keine Schreibsperre (keine Pflicht-Locks). Dort
  bleiben die Lock-Datei-Prüfung und die Konsistenz von Hash und Bytes durch
  den Snapshot.

## Considered Options

- **Schreibsperre für die ganze Upload-Dauer halten, ohne Snapshot.** Ein
  Upload über mehrere Minuten würde Access beim Öffnen blockieren („Datei wird
  verwendet“).
- **Sperre nur für bekannte Datenbanktypen.** Das schützt nur vor bekannten
  Fällen, siehe Entscheidung, Punkt 2.
- **VSS-Schattenkopie.** Braucht einen Dienst mit Adminrechten. Weil Jet kein
  VSS-Writer ist, wäre die Kopie auch nur so konsistent wie nach einem
  Stromausfall.
