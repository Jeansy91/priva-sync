# ADR-0019: WebDAV PROPPATCH ohne Dead-Property-Store (vorerst)

**Status:** Accepted
**Datum:** 2026-06-21

## Kontext

Der WebDAV-Endpoint (ADR-0018) deckt PROPFIND/PUT/GET/MKCOL/DELETE/MOVE/COPY/
LOCK/UNLOCK ab, inklusive content-hash-ETag und RFC-7232-Preconditions
(If-Match/If-None-Match). Offen bleibt **PROPPATCH** mit **Dead Properties** —
beliebige, vom Client gesetzte XML-Eigenschaften (z. B. macOS-Finder-Metadaten,
Windows-`Win32*`-Zeitstempel).

`golang.org/x/net/webdav` persistiert Dead Props nur, wenn die `File` das
Interface `DeadPropsHolder` (`DeadProps()` + `Patch()`) implementiert. Ohne das
beantwortet die Bibliothek PROPPATCH RFC-konform mit einem `207 Multi-Status`,
in dem die betroffenen Properties als fehlgeschlagen markiert sind. Die meisten
Clients (Rclone, Finder, Windows-Explorer) tolerieren das.

Eine echte Dead-Prop-Persistenz erfordert:
- eine neue Tabelle (`webdav_dead_props(sync_root_id, path, props jsonb)`) plus
  Repository,
- **Lifecycle-Kopplung** an jede Mutation: MOVE/COPY müssen Props mitziehen,
  DELETE/Trash sie mitnehmen, Restore wiederherstellen — sonst verwaisen sie,
- WebDAV-**spezifische** Metadaten im ansonsten protokollneutralen File-Modell.

## Entscheidung

PROPPATCH-Dead-Props werden **vorerst nicht persistiert**. Das aktuelle
Verhalten (per-Property-Failure im `207`) bleibt bestehen — RFC-4918-konform und
von den Ziel-Clients toleriert.

Wird ein konkreter Ziel-Client (Finder/Windows-Netzlaufwerk) dadurch
nachweislich gebrochen, kommt der Dead-Prop-Store als **eigene ADR + Slices**:
dann mit klarer Lifecycle-Strategie (MOVE/COPY/Trash/Restore) statt nachträglich
drangeflanschter Tabelle.

## Verworfene Alternativen

- **`jsonb`-Tabelle jetzt einführen.** Schema + Lifecycle-Kopplung an alle
  Mutationen für einen unbelegten Bedarf → Tiefe ohne Nachfrage, Schuld auf
  Verdacht. Verschoben, nicht verworfen.
- **PROPPATCH `200 OK` antworten ohne zu speichern.** Stiller Datenverlust: der
  Client glaubt, die Property sei gesetzt. Unehrlich, abgelehnt.

## Konsequenzen

**Positiv:**
- Kein WebDAV-spezifischer Ballast im File-Modell; keine ungetestete
  Lifecycle-Kopplung.
- Verhalten ist RFC-konform und für Rclone/Finder/Windows in der Praxis
  ausreichend.

**Negativ:**
- Clients, die zwingend Dead Props schreiben wollen, sehen Teil-Fehler im
  PROPPATCH-`207`. Falls real störend → Folge-ADR.
