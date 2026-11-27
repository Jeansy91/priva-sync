# ADR-0046: Eine Session endet nur bei Ablehnung — in jedem Client, und die Rotation bekommt ein Kulanzfenster

**Status:** Accepted
**Datum:** 2026-08-26
**Ersetzt:** [ADR-0042](0042-session-survives-refresh-races-and-blips.md)

## Kontext

ADR-0042 hat das Rotations-Race und die untypisierten Refresh-Fehler im
Desktop-Client gelöst und ein serverseitiges Kulanzfenster ausdrücklich
verworfen: eine Client-Schwäche rechtfertige kein Aufweichen der Garantie
"ein Refresh-Token, ein Gebrauch".

Diese Annahme hat nicht gehalten. Derselbe Ausfall trat im Browser auf: Die
Dateiseite feuert beim Öffnen eines Ordners rund ein Dutzend Requests
gleichzeitig (`/access`, `/shares` je Sync-Root, `/directories/stats`). Läuft
das Access-Token ab, laufen alle in ein 401, und
`HttpClient.request` startete pro 401 einen eigenen Refresh — mit demselben
Cookie. Der erste rotierte, alle weiteren bekamen 401 und meldeten den Nutzer
ab. Dazu kam dieselbe zweite Ursache wie im Desktop: `setRefreshFn` fing jeden
Fehler und lieferte `null`, also endete auch ein 502 oder ein WLAN-Wechsel die
Session. Und selbst ein erfolgreicher Refresh landete nur im `HttpClient`,
nie im Auth-Store — nach einem Reload lag wieder das abgelaufene Token in
`localStorage`.

Damit ist klar: Das Race ist keine Eigenheit eines Clients, sondern eine
Eigenschaft des Protokolls. Jeder Client, der mehr als einen Request parallel
schickt, muss es lösen — Desktop, Web, und jeder künftige. Ein Vertrag, den
jeder Teilnehmer einzeln fehlerfrei implementieren muss, ist der falsche
Vertrag.

## Entscheidung

Eine Session endet ausschliesslich, wenn der Server den Refresh-Token
**ablehnt** (401). Drei Bausteine setzen das durch, zwei im Client, einer im
Server:

- **Single-Flight je Client.** Parallele 401er kollabieren zu genau einem
  Refresh; die Wartenden übernehmen dessen Ergebnis. Im Desktop über den
  Access-Token als Refresh-Epoche unter dem Write-Lock (unverändert aus
  ADR-0042), im Web über `HttpClient.refreshOnce` plus den Epochen-Vergleich
  `this._accessToken !== sentWith`: Ein 401 auf ein bereits überholtes Token
  wird ohne Refresh einfach wiederholt.
- **Typisierte Refresh-Fehler.** `RefreshResult` unterscheidet `refreshed`,
  `unauthorized` und `unavailable`. Nur `unauthorized` beendet die Session;
  `unavailable` (Transport, 5xx, geworfene Ausnahme) reicht den ursprünglichen
  Fehler an den Aufrufer durch und lässt die Credentials stehen. Das
  erneuerte Token geht durch den Auth-Store, nicht am ihm vorbei, damit
  Persistenz und alle Token-Konsumenten denselben Stand sehen.
- **Kulanzfenster bei der Rotation.** `POST /auth/refresh` löscht den alten
  Token nicht mehr, sondern markiert ihn (`refresh_tokens.rotated_at`).
  Innerhalb von `auth.refresh_rotation_grace` (Default 30 s) liefert er noch
  ein Access-Token, **rotiert aber kein zweites Mal** — sonst würde er genau
  das Cookie entwerten, das der Client eben bekommen hat. Danach wird er
  abgelehnt und gelöscht. Nach jeder Rotation räumt ein Sweep die abgelaufenen
  markierten Zeilen desselben Nutzers weg.

Das Kulanzfenster ist nicht der Ersatz für den Single-Flight, sondern seine
Absicherung: Der Client vermeidet das Race, der Server verzeiht es.

## Konsequenzen

- Ein Logout heisst tatsächlich: der Server hat die Credentials abgelehnt.
  Netzwerk-Aussetzer, Server-Neustarts und Request-Pulks überleben — in jedem
  Client, auch in einem, der den Single-Flight noch nicht hat.
- Die Garantie "ein Refresh-Token, ein Gebrauch" gilt nur noch ausserhalb des
  Fensters. Ein gestohlener Refresh-Token ist damit für 30 s nach seiner
  Rotation noch etwas wert — allerdings ausschliesslich für ein Access-Token,
  nicht für eine eigene Token-Kette: Der Dieb bekommt kein neues Refresh-Token
  und ist nach Ablauf des Access-Tokens draussen. Der Preis ist bewusst
  gewählt und über `auth.refresh_rotation_grace` justierbar; `0` stellt das
  alte Verhalten wieder her.
- Der Sweep hält `refresh_tokens` so klein wie vorher. Ohne ihn läge jede
  rotierte Zeile bis zum Ende der Refresh-Token-TTL (30 Tage) herum.
- `RefreshResult` ist eine Breaking Change an `ApiClient.setRefreshFn`. Der
  einzige Aufrufer ist der Web-Auth-Store.
- Weiterhin nicht abgedeckt: der proaktive Refresh vor Ablauf. Solange rein
  401-getrieben erneuert wird, entsteht bei jedem Ablauf ein Pulk paralleler
  401er — jetzt an drei Stellen entschärft, aber immer noch unnötig.
