# ADR-0050: Der Desktop-Client schreibt ein rotierendes Dateilog

**Status:** Accepted
**Datum:** 2026-08-27

## Kontext

Der Desktop-Client hat bis hierher ausschließlich nach stdout geloggt. Ein
Release-Build ist ein `windows_subsystem = "windows"`-Binary ohne angehängte
Konsole — jede Warnung, die er ausgab, ging ins Leere.

Das ist keine theoretische Lücke. Ein frisch gemappter Sync-Root lieferte auf
einem anderen Host nur Verzeichnisse, keine Dateien. Weder Fehlermeldung noch
`failed_server_changes`-Zeile, weil `CfCreatePlaceholders` seine Per-Entry-Fehler
nur im `Result`-Feld meldet und der Aufrufer das nicht prüfte. Die Diagnose lief
über die SQLite-Datei des Clients und den Changes-Feed des Servers — nicht über
das, was der Client selbst gesehen hatte. Ein Log hätte die Ursache in einer
Zeile gezeigt.

Verworfen: `tauri-plugin-log`. Der Client loggt bereits durchgängig über
`tracing`; der Plugin-Weg würde eine zweite Fassade danebenstellen, statt die
vorhandene an einen Writer zu hängen.

## Entscheidung

**Zwei Layer über einer `tracing_subscriber::registry`, mit je eigenem Filter.**
Konsole behält `privasync=debug,warn` (überschreibbar via `RUST_LOG`), die Datei
bekommt `privasync=info,warn` (überschreibbar via `PRIVASYNC_LOG`). Getrennte
Per-Layer-Filter, weil ein globaler Filter beiden Layern die permissivste Stufe
aufzwingt — die Datei bekäme das Konsolen-Volumen. Konkret hält das den
`transfer_speed`-Sampler (im Sekundentakt, rund 86 000 Zeilen pro Tag) aus der
Datei heraus.

**Tägliche Rotation, sieben Dateien Retention** über
`tracing_appender::rolling`. Eine Woche deckt "Freitag kaputtgegangen, Montag
gemeldet" ab, ohne dass eine große Erstsynchronisation unbegrenzt wächst. Der
Writer ist `non_blocking` — der Sync-Loop wartet nicht auf Datei-I/O; der
`WorkerGuard` wird bewusst geleakt, weil er die gesamte Prozesslaufzeit
überdauern muss.

**Initialisierung vor dem Tauri-Builder**
([`logging::init`](../../apps/desktop/src-tauri/src/logging.rs)), damit
Start-up-Fehler mit im Log landen. Das Verzeichnis wird deshalb selbst aufgelöst
statt über `AppHandle::path().app_log_dir()`; `log_dir()` bildet Tauris Regel pro
Plattform nach (Windows `%LOCALAPPDATA%\<identifier>\logs`).

**Jeder Fehlschlag der Dateiseite degradiert auf Konsole**, nie auf einen
Abbruch: kein auflösbares Verzeichnis, kein anlegbares Verzeichnis, kein
baubarer Appender.

## Konsequenzen

Das Log enthält Dateipfade, Sync-Root-Namen und Serveradressen — es liegt
ausschließlich lokal im Benutzerprofil und wird nirgendwohin übertragen. Wer es
zur Fehlersuche weitergibt, gibt seine Ordnerstruktur mit. Die
Sieben-Tage-Retention begrenzt, wie weit das zurückreicht.

Der Identifier steht nur in `tauri.conf.json`; `logging.rs` liest ihn seit
ADR-0087 über `app_id::identifier()` von dort.

Eine Million Dateien in der Erstsynchronisation schreiben auf `info` rund eine
Zeile pro Datei. Das ist der Preis dafür, dass genau dieser Lauf nachvollziehbar
ist; die Rotation räumt es nach einer Woche weg.
