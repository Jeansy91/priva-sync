# ADR-0063: Verschieben wird an der Dateiidentität erkannt, nicht am Namen

**Status:** Accepted
**Datum:** 2026-09-03

## Kontext

Der Watcher baut ein **Verschieben** ausschliesslich aus
`ModifyKind::Name(RenameMode::Both)`. Dieser Zweig ist auf Windows und macOS
**unerreichbar**: `notify` 7.0.0 erzeugt `Both` nur im inotify-Backend, also
nur unter Linux. Eine Probe auf dem betroffenen Host bestätigt es:

```
Datei  B.txt → A.txt : Modify(Name(From)) … Modify(Name(To))   tracker=None
Ordner sub   → sub2  : Modify(Name(From)) … Modify(Name(To))   tracker=None
```

Zwei unverbundene Ereignisse, kein Cookie, das sie zusammenhält. Damit ist der
gesamte Server-Move-Pfad auf diesen Plattformen toter Code — in sieben Tagen
Log dieses Clients steht kein einziges `server move:`. Jede Umbenennung wird
zu Löschen plus Neu-Anlegen, und die Datei bekommt serverseitig eine **neue
File-Id**. Andere Clients erkennen den Vorgang deshalb ihrerseits nicht als
Verschieben; sie laden die Bytes erneut und hängen davon ab, dass der
Tombstone des alten Pfads sauber durchkommt. Am 01.09.2026 tat er das nicht
([ADR-0062](0062-offener-aenderungs-auftrag-benennt-den-pfad.md)).

Zwei Folgeschäden derselben Zeile:

- `RenameMode::To` wird mit `.filter(|p| p.is_file())` verworfen. Bei einem
  **Ordner** fällt die Zielseite damit komplett weg, während `From` zu einer
  Teilbaum-Löschung wird. Ordner-Umbenennungen verlieren die neue Seite.
- Jedes Verschieben überträgt die vollen Bytes neu. Bei einem Ordner den
  ganzen Teilbaum — bei den Roots, für die ADR-0037 ausgelegt ist, sind das
  Millionen Dateien für eine Operation, die serverseitig ein UPDATE wäre.

## Entscheidung

**Ein Verschieben wird über die Dateiidentität erkannt** — NTFS-FileIndex,
sonst inode+device — die als Spalte in `local_files` mitläuft. Das ist das
einzige Band zwischen altem und neuem Pfad, das das Betriebssystem
tatsächlich hergibt.

**Ein `From`-Ereignis bedeutet nicht „gelöscht", sondern „von diesem Pfad
verschwunden".** Dafür gibt es einen eigenen `sync_status`, der dem Server
nichts sagt. Aufgelöst wird er zu Beginn des nächsten Sync-Zyklus, durch einen
Join gegen die frisch gestagten Zeilen: findet sich dort dieselbe
Dateiidentität, war es ein Verschieben, sonst eine Löschung. Gestagt heisst
hier Upload *oder* angelegtes Verzeichnis — ein umbenannter Ordner erscheint
als `DirCreated`, nicht als Upload.

**Ein verschwundenes Verzeichnis beschattet seinen Teilbaum.** Windows meldet
für einen Ordner-Rename nur das Verzeichnis, kein Ereignis pro Kind. Zwischen
`From` und der Auflösung zeigen die Kind-Zeilen deshalb auf die alten Pfade,
während die Bytes schon unter dem neuen liegen. Ein **Voll-Walk** oder
**Sicherheitsnetz-Lauf** in diesem Fenster würde den gesamten alten Teilbaum
als gelöscht stagen und den neuen als unbekannt hochladen — genau der Schaden,
den diese Entscheidung beseitigen soll, nur in einem kürzeren Fenster.
Unterhalb eines verschwundenen Verzeichnisses staged der Walk deshalb nichts.

**Damit fällt jede Zeitannahme weg.** Kein Paarungsfenster, keine Karenz, keine
Konstante, die jemand später falsch einstellt. Ein spät eintreffendes `To`
findet der Join genauso, und ein ganz ausgefallenes `From` — Kernel-Puffer
übergelaufen — braucht er gar nicht.

**Ein Verschieben wird beim ersten Blick aufgelöst, eine Löschung erst beim
zweiten.** `From` und `To` treffen als zwei getrennte Ereignisse ein; startet
ein Zyklus dazwischen, fände der Join keinen Partner und schlösse fälschlich
auf eine Löschung. Eine verschwundene Zeile wird deshalb erst zur Löschung,
wenn sie einen Zyklus ohne Partner überstanden hat. Das kostet den Move nichts
— er wird sofort erkannt — und verzögert nur die Löschung um einen Zyklus.
Auch das ist keine Zeitkonstante, sondern ein zweiter Blick.

**Damit konvergieren die beiden Ereignispfade.** Heute gehen Ereignisse, die
während eines laufenden Zyklus eintreffen, durch `stage_event` statt
`handle_file_event`, und der Kommentar an `sync_all_roots_draining` hält fest,
dass ein so beobachtetes Rename „goes out as delete + upload rather than as a
move" — weil `handle_file_event` auf einen Server-Move warten würde und den
laufenden Zyklus blockierte. Wenn nur noch Zustand geschrieben und die
Entscheidung im Join getroffen wird, entfällt der Unterschied: beide Pfade
schreiben dasselbe, und niemand wartet mehr in einem Ereignis-Handler.

Die Erkennung sitzt deshalb in der Engine, nicht im Watcher: sie liest die
lokale DB, und `translate_event` ist bewusst rein und zustandslos.

Verworfen: zeitliches Paaren von `From` und `To`. Es rät. Zwei Umbenennungen
im selben Fenster können sich kreuzen, und eine Fehlpaarung verschiebt
serverseitig die falsche Datei. Ebenfalls verworfen: der Inhalts-Hash als
**alleiniger** Paarungsschlüssel — in den Roots dieses Nutzers liegen 205
byte-gleiche `shaderdb.PcDx12` und 96 byte-gleiche `house_b.jpg`, ein
Hash-Suchlauf über alle verschwundenen Zeilen fände darunter beliebig viele
Kandidaten. Als **Bestätigung** eines bereits über die Dateiidentität
gefundenen einzelnen Kandidaten bleibt der Hash brauchbar — dort steht kein
Suchlauf mehr an, nur ein Vergleich der einen Zeile, die der FileIndex schon
benannt hat.

**`To` verzweigt nach Datei und Verzeichnis.** Der heutige
`.filter(|p| p.is_file())` verwirft die Zielseite eines Ordner-Renames
ersatzlos; ihn nur zu streichen wäre falsch, weil ein Verzeichnis dann als
`Modified` und damit als Datei-Upload gestagt würde. Ein `To` auf einem
Verzeichnis wird `DirCreated`, auf einer Datei `Modified`. Die Identität eines
Verzeichnisses verlangt auf Windows `CreateFileW` mit
`FILE_FLAG_BACKUP_SEMANTICS` — ohne das Flag lässt sich für den Handle keine
Information holen.

**Die Identität ist ein Hinweis, kein Beweis.** NTFS vergibt einen FileIndex
nach dem Löschen erneut, ReFS zählt anders, Netzlaufwerke und FAT liefern
keinen. Ein Treffer wird über Grösse **und Inhalts-Hash** zusätzlich
abgesichert, nicht nur über die Grösse — ein wiederverwendeter FileIndex bei
zufällig gleicher Grösse ist sonst ein stiller Fehltreffer, der die falsche
Zeile umbenennt. Der Hash kostet hier nichts Zusätzliches: `stage_upload`
hasht eine neue Datei ohnehin, sobald keine bekannte Zeile am Pfad liegt
([events.rs:282](../../apps/desktop/src-tauri/src/sync/events.rs#L282)) —
das ist genau der Fall einer frisch gestagten `To`-Zeile. Der Join vergleicht
also einen Hash, der beim Staging schon vorliegt, gegen den zuletzt bekannten
Hash der verschwundenen Zeile. Ohne verfügbare Identität bleibt es beim
heutigen Verhalten. Dasselbe Degradieren, das die id-basierte
Rename-Erkennung auf der Empfängerseite schon kennt.

Bestätigt durch einen Blick in NextClouds Desktop-Client (`discovery.cpp`):
dessen lokale Rename-Erkennung ist ebenfalls inode-basiert, sichert einen
Treffer aber zusätzlich per Checksum ab (`computeLocalChecksum`), nicht nur
über Grösse und `modtime`. Diese Entscheidung zieht nach.

**Die Spalte wird lazy gefüllt**, bei Upload, Download und Walk-Berührung. Eine
Migration, die einen Millionen-Root durchstattet, gibt es nicht. Der Index auf
die Spalte entsteht erst nach dem Erstsync — sonst zahlt genau der Lauf, den
ADR-0037 schnell gemacht hat, die Indexpflege für eine Million Inserts.

## Konsequenzen

**Positiv**

- Verschieben bleibt Verschieben, auf allen Plattformen. Die Server-File-Id
  bleibt erhalten, andere Clients erkennen es wieder als solches.
- Ordner-Umbenennungen funktionieren erstmals. Windows meldet für einen Ordner
  ein einziges From/To-Paar, nicht eines pro Kind — aus einer Million
  Übertragungen wird ein `move_file` und serverseitig ein `MoveDirectory`.
  Erkannt wird das im nächsten Sync-Zyklus, nicht erst im nächsten Voll-Walk;
  bisher wurde ein umbenannter Ordner als Löschung behandelt und die neue Seite
  gar nicht bemerkt.
- Keine neue Zeitkonstante.
- Eine Umbenennung, die während eines Ausfalls des Watchers passiert, ist auch
  im **Voll-Walk** als solche erkennbar; heute wird sie dort zu Löschen und
  Neu-Hochladen.

**Negativ**

- Eine Spalte und ein Index mehr auf einer Tabelle, die für Millionen Zeilen
  ausgelegt sein muss.
- Ein `stat` je `To`-Ereignis.
- Ein neuer `sync_status`, den jede Stelle kennen muss, die über Zeilenzustände
  entscheidet — der Walk eingeschlossen, der ihn zusätzlich als Beschattung
  eines ganzen Teilbaums lesen muss und nicht nur als Zustand einer Zeile.
- Bleibt ein verschwundenes Verzeichnis unaufgelöst liegen — Absturz zwischen
  `From` und Zyklusbeginn —, ist sein Teilbaum so lange für den Walk unsichtbar.
  Der Zustand braucht deshalb einen Weg zurück, wenn beim Zyklusbeginn weder
  ein Move-Partner noch der alte Pfad existiert.
- Zeilen ohne Identität — Altbestand, exotische Dateisysteme — verhalten sich
  weiter wie heute. Die Verbesserung wächst mit der Zeit hinein, statt sofort
  vollständig zu sein.
- Löschungen erreichen den Server einen Zyklus später als heute.
- **Auf der Empfängerseite bleibt ein Ordner-Move nicht O(1).** `MoveDirectory`
  schreibt jede Nachfahren-Zeile um, und der `files_assign_seq`-Trigger
  (Migration 032) vergibt dabei jeder ein neues `seq` — der gesamte Teilbaum
  läuft also erneut durch den Changes-Feed. Der Empfänger braucht davon genau
  eine Zeile, um die Dateisystem-Umbenennung zu machen; die übrigen sind
  DB-Fixups. Ein Feed, der einen Teilbaum-Move als **einen** Eintrag ausdrückt,
  wäre eine Protokolländerung an ADR-0036 und ist hier bewusst nicht Teil der
  Entscheidung — in ADR-0065 entschieden, samt der schärferen Folge, dass
  ein Move jenseits von `MAX_DEFERRED_TOMBSTONES` die Tombstone-Paarung beim
  Empfänger blind werden lässt.

## Referenzen

- [ADR-0062](0062-offener-aenderungs-auftrag-benennt-den-pfad.md) — die
  Empfängerseite desselben Vorfalls
- ADR-0037 — Millionen-Datei-Roots; begrenzt, was eine Migration kosten darf
- `docs/glossary.md` — **Verschieben**, **Dateiidentität**,
  **Verschwunden**
- notify 7.0.0, `src/windows.rs` / `src/inotify.rs` — `RenameMode::Both`
  existiert nur im inotify-Backend
- [nextcloud/desktop `discovery.cpp`](https://github.com/nextcloud/desktop/blob/master/src/libsync/discovery.cpp) —
  inode-basierte Rename-Erkennung mit zusätzlicher Checksum-Bestätigung, Vorbild
  für die Hash-Absicherung oben
