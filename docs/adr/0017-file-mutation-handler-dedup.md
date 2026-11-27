# ADR-0017: File-Mutation-Handler-Dedup via MutationContext, Coordinator bleibt der tiefe Kern

**Status:** Accepted
**Datum:** 2026-06-20

## Kontext

Die HTTP-Handler für mutierende File-Operationen existieren doppelt: einmal für
eigene Sync Roots (`files_mutate.go`) und einmal für **Shared Mounts**
(`shared_mounts_mutate.go`). Own-Root-Upload und Mount-Upload sind nahezu
identische Zwillinge — Quota-Pregate, Staging-Write, `mtime`-Parse,
If-Match-Parse, Coordinator-Aufruf, Staging-Cleanup und der 3-Wege-Conflict-
Switch stehen wortgleich an beiden Stellen (Move/Delete analog). Es variiert nur
zweierlei: die Pfad-Auflösung (`GetSyncRoot` vs. `resolveMount` mit
Share-Escape-Guard) und die owner/actor-Zuordnung (own-root: `UserID=ActorUserID`;
mount: owner=Geber, actor=Empfänger, ADR-0013).

Der **Sync Coordinator** ist bereits tief und sauber: seine Cmds tragen alle
denselben Kopf (`UserID`, `ActorUserID`, `SyncRootID`, `Path`) — also das schon
aufgelöste Mutationsziel. Die Duplikation sitzt nicht im Coordinator, sondern
eine Schicht darüber in den HTTP-Adaptern.

Verworfen: ein generisches `MutationTx`-Template im Coordinator (zwingt die pro
Mutation absichtlich verschiedene Outbox-/Bus-Choreografie auf einen Lowest-
Common-Denominator → führt Schuld ein, statt sie abzubauen).

## Entscheidung

Die Duplikation wird **adapter-seitig über dem Coordinator** beseitigt, ohne den
Coordinator anzufassen:

- Ein Wert-Typ `MutationContext{ Owner, Actor, RootID }` — exakt der Kopf, den
  jedes Cmd schon trägt. **Kein Interface, keine Laufzeit-Polymorphie** (der
  Handler weiß statisch, ob own-root oder mount).
- Zwei freie Resolver `resolveOwnRoot(r)` und `resolveMount(r)`, die
  `(MutationContext, basePath)` liefern. Der sicherheitskritische
  `isUnderShareRoot`-Escape-Guard lebt **in** `resolveMount` und wird dadurch
  eigenständig testbar.
- Ein privater Shared-Body pro Mutationsfamilie (z. B. `uploadResolved`), der
  Staging → Coordinator → Render einmal kapselt.
- Ein privater Render-Helfer `writeUploadResult(w, UploadResult)` für den
  Conflict-Switch — getrennt, weil ihn drei Eintrittspunkte teilen (PUT
  own-root, POST mount, chunked-complete), der chunked-Pfad aber ohne Staging.

`MutationContext` ist ein **Code-Struct, keine Domain-Sprache** — er kommt
bewusst **nicht** im Glossar (`docs/glossary.md`).

Geltungsbereich: die `MutationContext`-Resolver bedienen **alle** Mutationen
(upload/move/delete/copy, gemeinsamer Kopf). Der unifizierte Render-Helfer gilt
**nur** für Upload — Move-/Delete-Responses divergieren absichtlich
(`current_path` statt `path`, kein `server_hash`) und bekommen erst einen
eigenen Helfer, wenn dort echter Schmerz auftaucht.

## Konsequenzen

**Positiv:**
- Upload-/Move-/Delete-Choreografie steht je einmal statt 2–3× kopiert
  (Locality). Neue Mutation = ein Resolver-Aufruf + Domain-Logik.
- Der Escape-Guard wird aus dem Mount-Handler gehoben und isoliert testbar —
  ein Sicherheits-Gewinn.
- Der Upload-Render ist über own-root, mount und chunked-complete derselbe
  Vertrag; Web (`saveText.ts`) und Desktop (`chunked.rs`) konsumieren bereits
  identische Felder, daher null Client-Risiko.
- Der Coordinator bleibt unangetastet das tiefste Modul.

**Negativ:**
- Upload und Move/Delete teilen den Render-Helfer **nicht** — bewusst, um die
  divergierenden Response-Formen nicht zwangs-zu-vereinheitlichen.
- Drei Upload-Eintrittspunkte bleiben bestehen (PUT/POST/chunked); sie teilen
  jetzt Body + Render, aber nicht die Routen-Verdrahtung.
