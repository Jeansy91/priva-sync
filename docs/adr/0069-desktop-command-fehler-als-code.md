# ADR-0069: Desktop-Commands melden Fehler als `{error, code}`

**Status:** Accepted
**Datum:** 2026-10-08

## Kontext

Der Server liefert Fehler als `{error, code}`; der Web-Client übersetzt `code`
über `translateApiError` (`errors.*`-Keys in `@privasync/i18n`). Die
Tauri-Commands des Desktop-Clients gaben dagegen `Result<T, String>` zurück:
Der Rust-API-Client reichte nur den englischen `error`-Text weiter, das
Frontend zeigte `String(err)`. Ergebnis waren rohe Texte wie
„invalid credentials“ im UI und in Rust fest verdrahtete deutsche Meldungen,
die die Sprachwahl ignorierten.

## Entscheidung

1. **`CommandError` (`src-tauri/src/command_error.rs`)** serialisiert als
   `{error, code}` — dieselbe Form wie ein Server-Fehler-Body. Jeder Command,
   dessen Fehler der Nutzer sieht und sinnvoll einordnen kann, gibt
   `Result<T, CommandError>` zurück.
2. **Server-Codes bleiben erhalten.** Der API-Client trägt sie als typisierten
   `ServerError` im `anyhow::Error`; `From<anyhow::Error>` holt den Code
   heraus. Für nutzerrelevante Requests liest `ensure_success` den Body, ohne
   den Fehlertext `"{op}: HTTP {status}"` zu ändern, auf den die
   Retry-Klassifikation per String prüft. 401 bleibt das typisierte
   `Unauthorized` (ADR-0025).
3. **Lokale Fehler bekommen Codes aus `command_error::codes`** und landen im
   selben `errors.*`-Namensraum. Hat ein Server-Code dieselbe Bedeutung
   (`SYNC_ROOT_NOT_FOUND`), wird er wiederverwendet.
4. **Ohne Code** (I/O, Datenbank, interne Invarianten) bleibt `error` der
   angezeigte Text. Unter Windows sind I/O-Meldungen bereits vom
   Betriebssystem lokalisiert.
5. **Frontend:** Jede Fehleranzeige eines Commands läuft über
   `commandErrorMessage` (`apps/desktop/src/commandError.ts`). Die Funktion
   akzeptiert beide Formen, also `{error, code}` und einen reinen String.
   Commands ohne nutzerrelevante Fehler dürfen deshalb bei `String` bleiben.

## Konsequenzen

- Rust sendet keine nutzersichtbaren Texte mehr. Übersetzungen leben
  ausschließlich in `@privasync/i18n`.
- Ein neuer lokaler Code braucht einen `errors.*`-Eintrag in `de.json` und
  `en.json` (`keyParity.test.ts` erzwingt den Gleichstand), sonst erscheint
  der englische Fallback.
