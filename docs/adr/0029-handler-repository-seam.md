# ADR-0029: Handler-Repository-Entkopplung ueber schmale Rollen-Interfaces

**Status:** Accepted
**Datum:** 2026-08-08

## Kontext

Die HTTP-Handler (`internal/api/handler`) haengen an **konkreten**
Repository-Typen (`*repository.UserRepository`, `*repository.FileRepository`,
`*storage.Service`, `*quota.Service`, …). Einzige Ausnahme ist
`syncpkg.Coordinator`, das bereits ein Interface ist.

Dadurch lassen sich die Fehlerpfade der Handler nicht gezielt testen: 152
`ErrInternal`-Stellen (HTTP 500) folgen dem Muster
`x, err := repo.Foo(ctx); if err != nil { writeError(500) }`, aber ein Test
kann eine *bestimmte* repo-Query nicht kontrolliert fehlschlagen lassen, ohne
die echte DB zu manipulieren. Ein abgebrochener Request-Context erreicht nur
die *erste* DB-Query eines Handlers (und auch die nur, wenn ihr Fehler auf 500
und nicht auf 404 abbildet) — geschaetzt ~40–60 der 152 Stellen. Der Rest der
500-Zweige ist mit der konkreten Verdrahtung unerreichbar.

Das ist zunaechst ein Testbarkeits-Defizit, aber die tiefere
Ursache ist Kopplung: die Handler-Schicht kennt pgx-gebundene Konkreta statt
Rollen. Entkopplung ist hier Selbstzweck genug (P6, Quality-Bar „testable"),
Coverage ist der willkommene Nebeneffekt — nicht umgekehrt.

## Entscheidung

1. **Handler haengen an schmalen Rollen-Interfaces, nicht an konkreten
   Repos.** Jeder Handler definiert im Handler-Package die Menge der
   Methoden, die er tatsaechlich nutzt (Interface Segregation), z.B.
   `authUserStore` mit genau den ~6 `UserRepository`-Methoden, die
   `AuthHandler` aufruft. Die konkreten `*repository.*`-Typen erfuellen diese
   Interfaces implizit (Go structural typing) — kein Adapter, keine
   Registrierung. Idiom: „accept interfaces, return structs".

2. **Keine fetten Ein-Interface-pro-Repo-Typen.** Ein einziges `UserStore`
   mit allen Methoden waere breit und zwaenge Fakes, viel zu stubben. Schmale,
   pro-Handler definierte Interfaces halten die Test-Fakes winzig und machen
   die tatsaechliche Abhaengigkeit jedes Handlers explizit.

3. **Inkrementell, ein Handler nach dem anderen (Tracer-Bullet).** Pro Schritt:
   Interface im Handler-Package einziehen → Konstruktor-Signatur auf das
   Interface umstellen → Router-/Test-Wiring anpassen → Fehler-Fake-Tests fuer
   die 500-Zweige → Coverage messen → committen. Jeder Schritt bleibt gruen und
   liefert Coverage. Kein Big-Bang-Diff.

4. **`storage.Service`, `quota.Service`, `mail.Mailer`, `audit.Repository`
   analog**, aber nachrangig — zuerst die repo-getriebenen 500-Zweige, weil
   dort die meisten unabgedeckten Statements liegen.

## Konsequenzen

- **Fehlerpfade werden vollstaendig testbar.** Ein Fake, der bei einer
  bestimmten Methode `err` liefert, deckt den zugehoerigen 500-Zweig gezielt
  ab — auch die zweite/dritte Query eines Handlers, die ein abgebrochener
  Context nicht erreicht. Erwartet: die Mehrzahl der 152 `ErrInternal`-Stellen
  wird abdeckbar.
- **Coverage-Erwartung ehrlich:** Der Seam bringt den Server-Kern auf
  geschaetzt ~79–80%. `setup.Complete` (nur auf frischer CI-DB abgedeckt) und
  `sse.Stream` (Streaming-Loop) bleiben Unit-technisch offen — der Rest ist
  Integrations-/E2E-Terrain, nicht Unit. 80% reine Unit-Coverage ist am Server
  realistisch die Obergrenze, nicht ein Zwischenschritt.
- **Handler-Konstruktoren aendern ihre Signaturen** (Interface- statt
  Konkret-Parameter). Rein additiv fuer Aufrufer, da die konkreten Repos die
  Interfaces schon erfuellen — das Router-Wiring uebergibt dieselben Objekte.
- **Kein neuer Test-Abhaengigkeits-Ballast** (kein `pgxmock`): Fakes sind
  handgeschriebene Structs im `_test.go`, die nur die gebrauchten Methoden
  implementieren.
- **Verworfen — abgebrochener Context als alleinige Loesung:** erreicht nur
  einen Teil der Zweige und etabliert ein zweites, schwaecheres Fehler-Idiom
  neben den Fakes (Duplikat). Als Vorstufe zu diesem Seam reine Doppelarbeit.
- **Verworfen — pgxmock/Pool-Interface:** verlangt Query-fuer-Query-Scripting
  in Reihenfolge (bruechig, verbose) und hilft nicht bei Storage-/Quota-/
  Mail-Fehlern.
