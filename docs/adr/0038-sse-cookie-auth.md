# ADR-0038: SSE-Stream authentifiziert über ein eigenes Cookie, nicht über die URL

**Status:** Accepted
**Datum:** 2026-08-24

## Kontext

Der Event-Stream `GET /api/v1/events` hat zwei Sorten Aufrufer mit
unvereinbaren Fähigkeiten:

- Der **Desktop-Client** (Rust) öffnet einen normalen HTTP-GET und kann den
  `Authorization`-Header setzen
  ([sse.rs:161](../../apps/desktop/src-tauri/src/sync/../sse.rs#L161)).
- Das **Web-Frontend** nutzt die Browser-API `EventSource`, die
  *keine* Request-Header erlaubt — weder bei der ersten Verbindung noch bei
  den automatischen Reconnects.

Bisher löste `extractBearer` das über einen Query-Parameter-Fallback: fehlte
der Header, wurde `?token=<access-token>` akzeptiert. Damit stand ein
vollwertiges, 15 Minuten gültiges Access-Token in der URL — und die URL wird
vom Request-Logger (`chimiddleware.Logger` protokolliert die komplette
RequestURI), von jedem Reverse-Proxy-Log, von der Browser-History und
potenziell von `Referer`-Headern mitgeschrieben.

Der Fallback war zudem nicht auf die Stream-Route beschränkt: er galt in der
`Authenticate`-Middleware und damit für **jeden** geschützten Endpunkt.

## Entscheidung

Der Query-Parameter-Fallback entfällt ersatzlos. Der Stream authentifiziert
sich stattdessen über ein eigenes Cookie:

- **Eigener Token-Typ** `TokenTypeSSE` (`auth.IssueSSEToken`). Ein
  Access-Token wird als Cookie **nicht** akzeptiert, ein SSE-Token als Bearer
  auf regulären Routen ebenfalls nicht. Ein entwendetes Cookie ist damit kein
  API-Vollzugriff, sondern erlaubt ausschliesslich das Mitlesen des eigenen
  Event-Streams.
- **Cookie** `sse_token`: `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/events`,
  `Secure` proxy-bewusst über `secureCookie()` (wie das Refresh-Cookie).
- **Lebensdauer = Access-Token-TTL.** Das Cookie wird in `issueTokenPair`
  gesetzt, also bei Login, 2FA-Abschluss und jedem Refresh gleichermassen neu
  ausgestellt, und beim Logout gelöscht. Es überlebt die Sitzung nicht.
- **Eigene Middleware** `AuthenticateSSE` nur für diese eine Route: Bearer
  (Access-Token) zuerst, sonst das Cookie. Die Route liegt deshalb ausserhalb
  der Bearer-only-Gruppe.

## Konsequenzen

**Positiv**

- Kein Credential mehr in URLs, Logs, History oder `Referer`.
- Reconnect-fest: `EventSource` verbindet nach einem Abbruch selbsttätig neu
  und sendet das Cookie automatisch mit. Ein Einmal-Ticket in der Query hätte
  genau hier versagt, weil `EventSource` dieselbe URL erneut anfordert.
- Der Desktop-Pfad bleibt unverändert.
- Blast Radius eines Leaks ist auf den Stream begrenzt.

**Negativ / Risiken**

- Ein Cookie ist grundsätzlich CSRF-Fläche. Hier akzeptabel: die Route ist
  GET-only und rein lesend, `SameSite=Strict` verhindert das Mitsenden bei
  Cross-Site-Navigationen, und CORS verhindert, dass eine fremde Origin den
  Stream-Inhalt liest. Ein Angreifer könnte allenfalls eine Verbindung
  *aufbauen*, ohne etwas davon zu sehen.
- Zweiter Authentifizierungspfad im Code. Bewusst eng gehalten: eine
  Middleware, eine Route, ein Token-Typ.
- `withCredentials: true` bleibt für `EventSource` erforderlich; die
  CORS-Konfiguration führt `AllowCredentials: true` bereits.
- `SameSite=Strict` setzt voraus, dass Web-Frontend und API dieselbe Origin
  teilen. Das ist der vorgesehene Betrieb (`server.static_path` lässt den
  Server das SPA selbst ausliefern) und gilt für das Refresh-Cookie bereits
  genauso. Wer das Frontend auf einer eigenen Origin betreibt, verliert
  beide Cookies — nicht nur dieses.

## Verworfene Alternativen

- **Kurzlebiges Einmal-Ticket in der Query.** Scheitert an den automatischen
  Reconnects von `EventSource`: nach Ablauf fordert der Browser dieselbe URL
  erneut an und bekommt 401, ohne dass die Anwendung eingreifen könnte, ohne
  den Fehler abzufangen und neu zu minten. Zudem stünde weiterhin ein
  Credential in den Logs.
- **Nur die Query im Logger schwärzen.** Behebt allein den Server-Log;
  Browser-History, `Referer` und Reverse-Proxy-Logs blieben unberührt.
