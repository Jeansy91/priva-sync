# ADR-0059: Live-Änderungen überholen die Walk-Warteschlange

**Status:** Accepted
**Datum:** 2026-08-30

## Kontext

Seit [ADR-0056](0056-walk-entkoppelt-uploads-laufen-mit.md) läuft der Voll-Walk
entkoppelt: er staged Zeilen, während die Uploads bereits laufen. Für den
Erstsync eines Roots mit Millionen Dateien heißt das, dass Discovery und
Transfer sich überlappen — beides zusammen dauert trotzdem Stunden bis Tage.

Genau in diesem Fenster arbeitet der Benutzer weiter. Eine Datei, die er jetzt
anlegt, ändert oder löscht, meldet der Watcher sofort; sie landet als `pending`
in derselben Warteschlange wie der Rest des Roots. Und die wurde `ORDER BY path`
gelesen. Eine Änderung an `\Z\notizen.txt` stand damit hinter allem, was
alphabetisch davor lag — praktisch hinter dem ganzen Walk.

Das Ergebnis ist der Zustand, den ein Sync-Client nicht haben darf: auf dem
Server liegt stundenlang eine ältere Fassung als auf dem Client, obwohl der
Client die neue längst kennt und sie als „ausstehend" anzeigt. Ein
WebDAV-Zugriff, ein zweites Gerät oder die Office-Vorschau sehen den alten
Stand. Der Erstsync ist ein Hintergrundjob; er darf den laufenden Betrieb nicht
anhalten.

Zwei Stellen verhinderten zusätzlich, dass eine Änderung überhaupt in die
Warteschlange kam, bevor der laufende Zyklus fertig war:

- Der Zyklus ist ein Zweig des `select!` in der Engine-Loop. Solange er
  awaited wird, wird der Zweig, der Watcher-Events liest, nicht gepollt. Beim
  Erstsync läuft ein Zyklus minutenlang — in dieser Zeit wurde kein einziges
  Event gestaged.
- Ein Batch umfasst `UPLOAD_BATCH_LIMIT = 2000` Zeilen. Eine Sortierung nach
  Vorrang wirkt nur an Batch-Grenzen; eine Änderung mitten im Batch hätte ihn
  komplett abwarten müssen.

## Entscheidung

**1. Die Upload-Warteschlange hat zwei Spuren.** `local_files.priority`: `1`
für alles, was der Watcher gemeldet hat, `0` für die Warteschlange des Walks.
`list_pending_batch` füllt den Batch Spur für Spur, innerhalb jeder Spur
weiterhin nach `path`. Mehr Abstufung gibt es bewusst nicht — Vorrang hat, was
gerade am Dateisystem passiert ist, unabhängig davon, ob es ein Anlegen, ein
Ändern oder ein Löschen war und ob der Server die Datei schon kennt.

**2. Alles, was der Watcher meldet, kommt in die Spur.** `stage_event` ist der
eine Trichter dafür (Watcher-Zweig *und* Debounce-Drain). Das Status-Prädikat
sitzt in `set_priority` selbst: Staging entscheidet oft, gar keine Zeile zu
schreiben (ignorierter Pfad, dehydrierter Platzhalter, Hash schon identisch),
und eine `synced`-Zeile zu markieren hinterließe einen Eintrag im Index, den
kein Übergang je wieder löscht.

**3. Die Spur räumt sich über die Endzustände.** `priority` wird auf `0`
zurückgesetzt, wo eine Zeile die Warteschlange verlässt (`set_synced`,
`set_synced_with_version`, `upsert_synced_with_version`, `set_conflict`,
`resolve_conflict` auf Server-Stand). Ein wiederholbarer Fehlschlag
(`pending_failed`, `blocked_remote_quota`) behält die Spur: die Datei ist
weiterhin die, auf die der Benutzer wartet.

**4. Ein partieller Index.** Nur Zeilen, die gerade in der Spur stehen, werden
indiziert — die Spurabfrage bleibt ein kleiner Indexscan in `path`-Reihenfolge,
egal wie viele Millionen Zeilen der Walk in Spur `0` gestaged hat. Spur `0`
selbst braucht keinen Index, sie läuft weiter über den Primärschlüssel wie die
unsortierte Abfrage vorher. Die Spurnummer steht als Literal im SQL, nicht als
Parameter: SQLite zieht einen partiellen Index nur heran, wenn der
Anweisungstext sein Prädikat impliziert, und `priority = ?` impliziert zur
Planzeit nichts.

**5. Der Watcher wird gedrained, während ein Zyklus läuft.**
`sync_all_roots_draining` pollt das Zyklus-Future gegen `event_rx`. Ein Event,
das nicht gestaged ist, kann von keiner Spur bedient werden — es steht gar
nicht in der Warteschlange. Gestaged wird, nicht voll behandelt: `stage_event`
ist synchrone DB-Arbeit, `handle_file_event` würde auf einen serverseitigen
Move warten und genau den Zyklus blockieren, neben dem es läuft. Ein hier
beobachteter Rename geht als Delete + Upload raus — dieselbe Abwägung, die der
Debounce-Drain schon immer trifft.

Das belebt **genau einen** der vier Zweige der Engine-Loop. Die Waisenprüfung
des Poll-Ticks, der Sicherheitsnetz-Walk und die Watcher-Neustart-Entscheidung
bleiben für die Dauer eines Zyklus ausgehungert, wie bisher. Sobald ein
zweiter Zweig mitten im Zyklus bedient werden muss, ist die Antwort kein
weiterer Wrapper, sondern der Schritt, den ADR-0056 für den Walk schon gemacht
hat: den Zyklus als eigenen Task absetzen und „ein Zyklus läuft" zu explizitem
Schleifenzustand machen.

**6. Ein laufender Batch bricht für eine Live-Änderung ab.** `set_priority`
meldet, ob es eine Zeile *neu* in die Spur geholt hat; nur dann zählt der
prozesslokale Zähler `lane_arrivals` hoch. Der Batch merkt sich dessen Stand
beim Start und vergleicht alle `PRIORITY_YIELD_EVERY = 100` geprüften Zeilen —
und zusätzlich nach jedem Upload-Häppchen von `UPLOAD_RUN_CHUNK = 200`
Dateien, denn ein Batch verbringt echte Zeit in beiden Hälften, und ein
Abbruch, der nur in der Vorprüfung greift, ließe eine Live-Änderung hinter bis
zu `UPLOAD_BATCH_LIMIT` Uploads warten. Bricht er ab, geht das bis dahin
Gesammelte raus, `sync_now` holt den nächsten Zyklus hoch, und der liest die
Spur zuerst.

Gezählt werden **Neuzugänge**, nicht „Spur nicht leer": eine Datei, die noch
geschrieben wird, wird bewusst zurückgestellt und bleibt in der Spur liegen,
und jeder weitere Schreib-Event holt sie erneut. Als Zugang gewertet, würde
jeder Batch sich selbst abbrechen, solange irgendwo kopiert wird. Der Zähler
statt einer Spur-Abfrage folgt demselben Muster wie `force_walk`: wer die Spur
füllt und wer sie beobachtet, ist derselbe Prozess — eine Abfrage müsste dafür
die DB-Mutex nehmen, auf der der abgesetzte Walk ohnehin schon sitzt.

## Alternativen

- **Ein `ORDER BY priority DESC, path`.** Müsste die gesamte Pending-Menge
  sortieren — beim Erstsync ist das der ganze Root. Genau die Materialisierung,
  die ADR-0056 überall sonst beseitigt hat.
- **`UPLOAD_BATCH_LIMIT` verkleinern.** Prüft die Spur häufiger, verteuert
  aber die Buchhaltung pro Batch (`count_pending` ist ein Indexscan über alle
  offenen Zeilen) proportional zur Verkleinerung. Der Abbruch kostet nur dann
  etwas, wenn tatsächlich eine Live-Änderung wartet. Sauberer wäre, Lesegröße
  und Abrechnungseinheit ganz zu trennen — der Nenner der Fortschrittsanzeige
  hat mit `TOTAL_REFRESH_INTERVAL` bereits seinen eigenen Takt. Solange beides
  dieselbe Zahl ist, bleibt es bei Häppchen innerhalb eines Batches.
- **Eine zweite, eigene Warteschlange für Watcher-Events.** Dupliziert
  Bulk-Delete-Guard, Settle-Prüfung, Retry-Fenster und Fortschrittszählung —
  alles, was an der Warteschlange hängt, gäbe es dann zweimal.
- **Den Walk pausieren, solange eine Live-Änderung ansteht.** Der Walk staged
  nur, er lädt nicht hoch; er ist nicht der Konkurrent. Zu pausieren wäre der
  Erstsync, und der ist das, was ohnehin am längsten dauert.

## Konsequenzen

- Eine Änderung während des Erstsyncs wartet auf höchstens ~100 Zeilen
  Vorprüfung oder ~200 laufende Uploads, statt auf den Rest des Walks. Der
  Abstand zwischen dem, was auf der Platte liegt, und dem, was der Server
  ausliefert, bleibt damit auch mitten in einem tagelangen Erstsync klein.
- Welche Bytes hochgehen, entscheidet weiterhin der Hash unmittelbar vor dem
  Upload, und eine noch wachsende Datei bleibt zurückgestellt: Vorrang ändert
  die Reihenfolge, nicht den Inhalt.
- Ein Batch kann kürzer ausfallen als `UPLOAD_BATCH_LIMIT`, und seine Uploads
  laufen in Häppchen statt in einem Zug. Der Durchsatz der Massen-Warteschlange
  sinkt dadurch messbar nur, wenn dauerhaft Live-Änderungen eintreffen — und
  dann ist genau das gewollt. Pro Häppchen fällt eine zusätzliche
  `count_pending`-Abfrage an; gegen 200 Netz-Uploads ist das Rauschen.
- `local_files` trägt einen zusätzlichen partiellen Index. Er ist im
  Ruhezustand leer, weil die Endzustände die Spur räumen.
