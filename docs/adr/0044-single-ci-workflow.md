# ADR-0044: Ein CI-Workflow, Builds hinter denselben Gates

**Status:** Accepted — Gate-auf-Tag-Regel und `concurrency` pro Ref abgelöst durch ADR-0053
**Datum:** 2026-08-26

## Kontext

Drei Workflow-Dateien prüften denselben Commit: `check.yml` (Tests),
`security.yml` (SAST, Audits, Container-Scan) und `release.yml`. Letzteres
enthielt **alle Jobs der beiden anderen als Kopie** — mit Absicht, damit ein
direkter Tag-Push auf einem ungeprüften Commit die Gates nicht umgehen kann.

Der Preis war Gleichzeitigkeit auf genau einem Runner. `make
release` pushte Branch und Tag getrennt, also feuerten Branch-Push
(`check` + `security`) und Tag-Push (`release`) zusammen; beim Release von
v0.5.0 kam noch ein zweiter `check`-Lauf auf demselben SHA dazu. Vier
gleichzeitige Läufe bedeuteten vier instrumentierte Rust-Coverage-Builds, zwei
komplette Go-Suiten mit je eigenem Postgres und zwei Server-Image-Builds.
Messbar: `test-desktop-rust` von 10:06 auf 22:55, ein Job blieb über sechs
Minuten im reinen Cache-Restore-Schritt hängen, und ein Web-Test fiel unter der
Last um.

## Entscheidung

**Ein Workflow** (`.github/workflows/ci.yml`) für Pull Requests, Pushes auf
`main` und Tags. Die Gate-Jobs sind dieselben Jobs, egal welcher Trigger; ein
Tag hängt die Build- und Publish-Jobs hinten dran, über `needs` auf **alle**
Gates plus `if: startsWith(github.ref, 'refs/tags/v')`.

Damit bleibt die Garantie aus `release.yml` wörtlich erhalten — ein Tag-Push
fährt alle Gates —, ohne sie zu duplizieren. `release.yml` war bereits die
richtige Struktur; sie wird zum Original statt zur Kopie.

**Die Blöcke sind eine Namenskonvention**, `test-*` gegen `sec-*`. Die
Workflow-Syntax kennt kein Gruppierungs-Primitiv für Jobs; Reusable
Workflows (`workflow_call`) wären das Mittel, werden hier aber nicht
genutzt. Fachlich ändert das nichts: Jobs ohne
`needs` starten ohnehin alle gleichzeitig.

**`concurrency` pro Ref, `cancel-in-progress` nur ausserhalb von Tags.** Ein
überholter Lauf ist auf einem einzelnen Runner nicht bloss nutzlos, er kostet
den nachfolgenden Lauf Zeit. Auf einem Tag darf nicht abgebrochen werden — ein
halb gepushtes Image ist schlimmer als ein doppelter Lauf.

**Werkzeuge als vorgebaute Binaries**, wo sie messbar wehtun:
`cargo-llvm-cov` und `cargo-audit` über `taiki-e/install-action` statt
`cargo install` aus den Quellen. Die Go-Tools (`tygo`, `gosec`, `gitleaks`)
bleiben bei `go install`; ihre Jobs liegen bei 1:30 bis 2:20 und rechtfertigen
die zusätzliche Versionspflege nicht.

**Das Server-Image wird über den geteilten Layer-Cache einmal bezahlt:**
`sec-trivy` baut es über buildx mit `cache-to: type=gha`, `server-image` liest
denselben Cache. Ob der GHA-Cache auf diesem Runner tatsächlich trägt, ist
nicht verifiziert — fällt er aus, degradiert das auf den vorherigen Zustand
(zwei Builds), nicht auf einen Fehler.

Verworfen: die Gates beim Tag-Lauf zu überspringen, wenn derselbe SHA schon
grün ist. Das ist deklarativ nicht ausdrückbar, und die Ausnahme wäre genau
der Pfad, auf dem ein ungeprüfter Tag durchrutscht.

## Konsequenzen

**Positiv**

- Ein Release fährt zwei Läufe statt vier, und die Gates existieren einmal
  statt dreimal — eine neue Prüfung wird an einer Stelle ergänzt.
- Aufeinanderfolgende Pushes auf `main` überholen sich, statt sich den Runner
  zu teilen.
- Builds laufen nur, wenn wirklich alles grün ist, inklusive
  `test-gen-events` — das war in `release.yml` bisher nicht in der
  `needs`-Liste.

**Negativ**

- **Ein Release erzeugt weiterhin zwei Läufe**, einen für `refs/heads/main` und
  einen für `refs/tags/vX`, und beide fahren die Gates. Das ist der Preis
  dafür, zwei Refs zu pushen; `make release` schickt sie jetzt in einem
  einzigen Push, was den Roundtrip spart, aber nicht das zweite Ref-Event.
  Der Tag-Lauf ist der, der zählt.
- Die `needs`-Liste steht dreimal wörtlich da. Workflow-Syntax kennt keine
  Anker; eine neue Gate muss in alle drei Listen.
- `taiki-e/install-action` ist eine neue Abhängigkeit auf einem Runner, der
  mit github.com bisher nur über einfache HTTPS-GETs spricht. Bricht sie, ist
  der Rückweg ein `cargo install`.

## Nachträge

**2026-08-26, noch am Tag der Annahme:** Die Entscheidung „Werkzeuge als
vorgebaute Binaries" ist **zurückgenommen**. Sie beruhte auf einer Ableitung
aus Nachbarjobs, nicht auf einer Messung, und der erste echte Lauf (467) hat
sie widerlegt:

- `test-desktop-rust` kam mit dem vorgebauten `cargo-llvm-cov` auf **10:35**,
  gegen eine Baseline von **10:06** mit dem Kompilat. Es war nichts zu holen —
  das `cargo install` war nie der dominierende Anteil.
- `sec-cargo-audit` ist daran **zerbrochen**. `taiki-e/install-action` liefert
  ein statisch gegen musl gebautes Binary, und das kann die Advisory-Datenbank
  nicht per git von github.com holen: „couldn't fetch advisory database: git
  operation failed: An IO error occurred when talking to the server". Das
  glibc-Binary aus `cargo install` holt sie auf demselben Runner problemlos —
  Lauf 464 grün um 07:26, Lauf 467 rot um 08:04. Einfache HTTPS-GETs nach
  github.com funktionieren hier, der git-Transport nicht; dieselbe Grenze, die
  der trivy-Job seit ADR-0031 dokumentiert.

Beide Jobs installieren wieder per `cargo install --locked`. Die Begründung
steht als Kommentar an beiden Stellen, damit der Versuch nicht ohne Messung
wiederholt wird.

Der Rest der Entscheidung — ein Workflow, Gates einmal, Builds nur bei Tag
hinter allen Gates, `concurrency` ohne Abbruch auf Tags — ist von Lauf 467
bestätigt: alle sechs `test-*` und fünf von sechs `sec-*` grün, ein Lauf statt
zwei.

**Ausserdem gehärtet:** `cancel-in-progress` ist als „positiv ein
Branch" formuliert statt als „nicht ein Tag". Löst der Ref nicht auf, wird
dadurch nichts abgebrochen und nichts gebaut, statt einen Release-Lauf
abzuwürgen.

**2026-08-26, zweiter Nachtrag — Jobs zusammengelegt.** Aus zwölf Gate-Jobs
sind acht geworden: `test-go` (gen-events + Server-Tests), `test-node` (alle
fünf TS-Suiten über einem `pnpm install`), `test-desktop-rust`, `sec-go`
(gosec + gitleaks), `sec-cargo-audit`, `sec-node`, `sec-semgrep`, `sec-trivy`.

Grund ist gemessen, nicht vermutet: die Startzeiten in Lauf 467 zeigen, dass
dieser Runner **etwa sechs Jobs gleichzeitig** annimmt — sechs starten
zusammen, die übrigen erst, wenn welche fertig werden. Zwölf Jobs bedeuteten
also Warteschlange **und** Verdrängung, und ausgehungerte Testprozesse fallen
als „flaky" auf (drei Web-Tests, alle mit derselben Timeout-Signatur). Jeder
Job ist ausserdem ein Container, ein Checkout und eine Toolchain-Installation;
vier Jobs zogen denselben `pnpm`-Baum viermal.

Wanduhrzeit kostet das nichts: `test-desktop-rust` ist mit ~10:30 der kritische
Pfad, und alles andere passt in seinen Schatten. Die zusammengelegten Ketten
bleiben darunter (`test-node` ~8 min, `test-go` ~4:15, `sec-go` ~3:40).

Damit keine Diagnose verloren geht, tragen die Folge-Schritte in `test-node`
und `sec-go` `if: always()` — eine fallende Web-Suite darf keine fallende
Desktop-Suite verdecken. In `test-go` bewusst **nicht**: der
`gen-events-check` ist ein Einzeiler-Fix, und dafür vier Minuten Testzeit
auszugeben lohnt nicht.

**Nicht gemacht, mit Begründung:** der Rust-Cache ist kein Hebel.
`test-desktop-rust` lag über drei aufeinanderfolgende Läufe bei 10:06, 10:35
und 11:09 — der mittlere und der letzte mit warmem Cache aus dem Lauf davor.
Coverage-Instrumentierung baut den Workspace ohnehin neu; es gibt hier keine
belegte Optimierung, also wird keine erfunden.

Ebenfalls offen und **ausserhalb des Repos**: die Zahl gleichzeitig
laufender Jobs des Runners. Von sechs auf drei zu senken würde die Verdrängung halbieren
und fast keine Wanduhrzeit kosten, weil die 10:30 des Rust-Jobs die Untergrenze
bleiben. Das ist der wirksamste verbleibende Hebel gegen die Flakes.

**2026-08-26, dritter Nachtrag — alle CI-Werkzeuge sind gepinnt.** Die
Entscheidung oben, `tygo`, `gosec` und `gitleaks` bei `@latest` zu belassen,
war falsch begründet: sie wog nur Laufzeit. Der entscheidende Punkt ist
Reproduzierbarkeit.

Lauf 475 wurde rot, ohne dass ein einziges Zeichen an der beanstandeten Datei
oder an der Scanner-Konfiguration geändert worden wäre — `gosec@latest` war
still zu einer Version geworden, deren Taint-Analyse
`handler/static.go:55` als G703 meldet. Ein Sicherheitswerkzeug, das die
Hauptlinie ohne Code-Änderung rot färbt, ist kein Gate, sondern ein Würfel.

Alle drei sind jetzt auf eine Version festgenagelt (`tygo@v0.2.21`,
`gosec@v2.29.0`, `gitleaks@v8.30.1`). Bei `tygo` steht dasselbe umgekehrt auf
dem Spiel: das Gate vergleicht generierte Ausgabe, eine neue
Generator-Version bricht es also ohne jede Struct-Änderung. Anheben geschieht
absichtlich, in einem Commit, der es sagt.

Der Fund selbst ist ein Falsch-Positiv und wurde **gezielt** unterdrückt, per
`#nosec G703` an der Zeile, mit Begründung — nicht über die globale
Exclude-Liste, die der Kommentar im Workflow zu Recht schützt. A/B belegt:
mit derselben Version meldet gosec ohne die Annotation ein Issue, mit ihr
keines. Der Schutz vor Traversal steht drei Zeilen darüber und weist jeden
Kandidaten ausserhalb des absoluten Roots mit 400 ab, bevor `os.Stat`
überhaupt erreicht wird; `filepath.Clean` auf einem absoluten URL-Pfad kann
ohnehin nicht nach oben entkommen.

Sauberer als die Annotation wäre `os.Root` (Go 1.24+): damit ist Traversal
nicht geprüft, sondern konstruktiv unmöglich, und die Unterdrückung entfiele.
Das ändert allerdings die Signatur von `NewSPAHandler` und einen
Request-Pfad — eigenes Ticket, nicht nebenbei.

## Referenzen

- ADR-0028 — Coverage-Basis und Schwellwerte (unverändert)
- ADR-0030 — Kalibrierung der Security-Gates (unverändert, nur umbenannt)
