# ADR-0071: Die Sync-Engine erreicht den Server nur über `AuthedApi`

**Status:** Accepted
**Datum:** 2026-10-14

## Kontext

[ADR-0025](0025-desktop-auth-refresh-seam.md) und
[ADR-0042](0042-session-survives-refresh-races-and-blips.md) haben mit
`with_auth_retry` festgelegt, wie ein 401 behandelt wird: einmal refreshen,
gleichzeitige 401 zu einem Refresh bündeln, danach die Anfrage wiederholen.
Benutzt haben das aber nur SSE, der Changes-Feed und ein Tauri-Command. Der
Rest der Sync-Engine lief an diesem Mechanismus vorbei:

- `run_uploads` zog **einen** `ApiSnapshot` pro Upload-Batch. Der Snapshot
  hält den Bearer-Token fest. Ein Batch läuft aber länger als das
  Access-Token gültig ist (15 min).
- Upload, Chunked-Upload, Delta, Verzeichnis-Anlage, Löschen, Move, Download,
  Hydration, Stat-Sweep, Root-Listing und der Share-Abgleich im
  Kontextmenü-Pipe riefen den Client direkt auf. Ein 401 löste dort keinen
  Refresh aus.
- `retry::is_transient_error` stuft 401 als permanent ein. `record_failure`
  setzte die Datei deshalb in den Backoff und parkte sie nach einigen Runden
  bis zum nächsten Client-Update.

Im Kundenlog vom 05.–10.10.2026 sieht man das deutlich: Ganze
200er-Batches scheiterten mit 401, erst der SSE-Reconnect (~alle 33 min)
refreshte den Token. Das waren rund 50.000 Fehler bei 16.890 Dateien, viele
davon geparkt.

## Entscheidung

**1. `AuthedApi` ist der einzige Weg der Sync-Engine zum Server.**
`SyncEngine.api`, `HydrationCtx.api` und `DaemonBackend.api` sind vom Typ
`AuthedApi` statt `Arc<RwLock<ApiClient>>`. Am rohen Client vorbei kommt die
Engine damit nicht mehr, das ist strukturell ausgeschlossen statt nur
Konvention.

**2. Ein Aufruf entspricht einer Anfrage, und jede Anfrage liest den aktuellen
Token.** `AuthedApi::call` nimmt den Snapshot beim Start der Anfrage,
`call_client` liest den Client unter seinem Lock. Beide laufen über
`with_auth_retry`. Mehrteilige Abläufe wie ein Chunked-Upload rufen pro
Teilschritt einzeln auf (init, status, jeder Chunk, complete, jeder
Assembly-Poll). So übersteht auch ein Upload, der länger dauert als ein Token
lebt, mehrere Refreshes.

**3. Auth-Fehler sind keine Dateifehler.** `api::is_auth_failure` erkennt
`Unauthorized`, `SessionExpired` und den jetzt typisierten
`NotAuthenticated`. Die Upload-Pfade lassen die Zeile in diesem Fall
unverändert in der Queue: kein `record_failure`, kein Backoff, kein Parken.

## Konsequenzen

- Upload-Batches laufen über das Ablaufen des Tokens hinweg weiter. Der
  Refresh hängt nicht mehr an SSE.
- Ist der Refresh selbst abgelehnt (Sitzung wirklich vorbei), feuert
  `session_expired` wie bisher. Alle weiteren Aufrufe scheitern sofort lokal
  an `NotAuthenticated`, ohne Netzwerkverkehr und ohne Dateien zu parken.
- Pro Anfrage kommt ein kurzer Read-Lock auf den `ApiClient` dazu. Gegen die
  Anfrage selbst ist das vernachlässigbar.
- `ApiSnapshot` bleibt der Request-Ausführer, wird in der Engine aber nicht
  mehr über eine einzelne Anfrage hinaus gehalten.
