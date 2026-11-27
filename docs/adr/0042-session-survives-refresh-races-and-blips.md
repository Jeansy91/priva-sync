# ADR-0042: Eine Session endet nur, wenn der Server den Refresh-Token ablehnt

**Status:** Superseded by [ADR-0046](0046-refresh-rotation-grace-window.md)
**Datum:** 2026-08-25

## Kontext

Der Desktop-Client meldete Nutzer mehrmals täglich ab, obwohl die Session
gültig war. Zwei unabhängige Ursachen, beide in
`with_auth_retry` ([api/mod.rs](../../apps/desktop/src-tauri/src/api/mod.rs))
bzw. `ApiClient::refresh_token`:

**1. Rotations-Race.** Der Server rotiert bei jedem `POST /auth/refresh` hart:
der alte Refresh-Token wird gelöscht, ein neuer ausgestellt
([auth_session.go](../../apps/server/internal/api/handler/auth_session.go)) —
kein Kulanzfenster, keine Wiederverwendung. Der Client hatte keinen
Single-Flight: Läuft das Access-Token ab, gehen SSE-Loop, Sync-Reconcile und
jedes Frontend-Command gleichzeitig in ein 401. Jeder Aufrufer las den
gespeicherten Refresh-Token **vor** dem Client-Lock, also alle denselben. Der
erste rotierte ihn, alle weiteren schickten einen bereits gelöschten Token und
bekamen zu Recht 401 — was der Client als "Session vorbei" auslegte.

**2. Jeder Refresh-Fehler galt als Session-Ende.** `refresh_token` warf für
Transport-Fehler, 5xx und 401 denselben untypisierten Fehler; `with_auth_retry`
löschte daraufhin Access-Token und signalisierte `session_expired`. Ein
WLAN-Wechsel, ein Server-Neustart oder ein 502 vom Reverse-Proxy genügte für
einen Logout mit vollständig gültigen Credentials.

## Entscheidung

Eine Session endet ausschliesslich, wenn der Server den Refresh-Token
**ablehnt** (401). Zwei Bausteine setzen das durch:

- **Access-Token als Refresh-Epoche.** `with_auth_retry` merkt sich den
  Access-Token, mit dem `op` lief. Beim 401 wird der Client-Write-Lock
  genommen und die Epoche **unter dem Lock** geprüft: hat sich der Token
  geändert, hat ein anderer Aufrufer bereits erneuert — dann wird nur
  `op` wiederholt, nicht ein zweites Mal refresht. Konkurrierende 401er
  kollabieren so zu genau einem Refresh. Das rotierte Paar wird noch unter dem
  Lock persistiert, damit kein anderer Pfad den neuen Access-Token zusammen mit
  dem überholten Refresh-Token sieht.
- **Typisierte Refresh-Fehler.** `refresh_token` nutzt `check_status`, liefert
  bei 401 also das typisierte `Unauthorized` (ADR-0025) und bei allem anderen
  einen gewöhnlichen Fehler. Nur `Unauthorized` löscht die Credentials und
  feuert `session_expired`; jeder andere Fehler lässt sie unangetastet und geht
  an den Aufrufer, dessen Backoff es erneut versucht.

Verworfen: ein Kulanzfenster serverseitig, in dem der alte Refresh-Token nach
der Rotation kurz weitergilt. Das weicht die Rotation für alle Clients auf, um
einen Fehler in genau einem Client zu umgehen — die Korrektheitsgarantie
"ein Refresh-Token, ein Gebrauch" bleibt so, wie sie ist.

## Konsequenzen

- Ein Logout heisst jetzt tatsächlich: der Server hat die Credentials
  abgelehnt. Netzwerk-Aussetzer und Server-Neustarts überleben.
- `with_auth_retry` nimmt vor jedem `op` kurz den Read-Lock, um die Epoche zu
  lesen. Unkontendiert, ohne messbaren Effekt.
- Die Epoche ist der Token-Wert selbst — kein zusätzlicher Zähler, kein
  zweiter Mutex, kein Zustand, der mit dem Token auseinanderlaufen kann.
- Nicht abgedeckt: der proaktive Refresh vor Ablauf. Solange rein
  401-getrieben erneuert wird, entsteht bei jedem Ablauf ein Pulk paralleler
  401er — jetzt harmlos, aber unnötig.
