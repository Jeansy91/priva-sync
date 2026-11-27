# ADR-0025: Desktop Auth-Refresh als API-Client-Seam

**Status:** Accepted
**Datum:** 2026-07-09

## Kontext

Die 401→Refresh→Retry-Orchestrierung des Desktop-Clients ist dreifach
kopiert (`sync/reconcile.rs`, `state.rs`, `sse.rs`). Jede Stelle matcht
per `contains("401")` auf den Fehlertext, kennt die Config-Key-Strings
für `refresh_token` und `device_id`, ruft `client.refresh_token(…)`,
speichert das neue Access-Token zurück, replayed den Aufruf und räumt im
Fehlerfall selbst auf (`clear_token()`, Config leeren,
`emit("session_expired")`). Der Refresh-Vertrag ist damit aus dem
API-Client in drei fachfremde Module geleakt — jede Änderung (Key-Namen,
Event-Name, Retry-Anzahl) muss dreimal nachgezogen werden und driftet.

## Entscheidung

Der Refresh-Vertrag zieht in den API-Client (`api/mod.rs`) hinter ein
einziges Interface:

- `with_auth_retry(op)`: führt `op` aus; auf 401 genau ein
  Refresh-Versuch (Refresh-Token + Device-ID aus der Config), danach
  Replay von `op`. Schlägt der Refresh fehl: Token und Config leeren,
  `session_expired` emittieren, Fehler durchreichen.
- 401-Erkennung typisiert über den Statuscode der API-Fehler-Variante,
  nicht über String-Matching im Fehlertext.
- Callsites (Reconcile, Commands, SSE-Connect) rufen nur noch
  `with_auth_retry(…).await?`. Für den langlebigen SSE-Stream gilt der
  Wrapper für den Verbindungsaufbau; ein Abriss mitten im Stream läuft
  über den bestehenden Reconnect, der wieder durch den Wrapper kommt.

## Konsequenzen

**Positiv:**

- Ein Vertrag statt drei Kopien; Änderungen an Keys, Event oder Policy
  haben einen Ort (Locality).
- Der Refresh-Pfad wird isoliert unit-testbar (op als Closure/Mock),
  statt in async Engine-Loops eingeschweißt zu sein.
- String-Matching auf Fehlertexte entfällt — eine stille Fehlerquelle
  bei Wortlaut-Änderungen.

**Negativ:**

- Der API-Client bekommt eine Abhängigkeit auf Config-Zugriff und
  Event-Emission (heute via Callback/Handle injizierbar zu halten,
  damit der Client testbar bleibt).
