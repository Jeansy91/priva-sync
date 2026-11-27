# ADR-0060: Clients aktualisieren sich beim Start selbst, vor allem anderen

**Status:** Accepted
**Datum:** 2026-08-30

## Kontext

Der Desktop-Client kannte bis hierher nur den Hinweis: nach dem Login prüfte
`updater::start_polling` einmal sofort und danach alle sechs Stunden gegen
`/updates/{target}-{arch}/{current_version}`, ein Fund erzeugte Banner,
Tray-Eintrag und eine OS-Benachrichtigung. Installiert wurde erst, wenn der
Benutzer in den Einstellungen auf „Installieren" klickte.

Damit hing die Version jedes Clients an zwei Bedingungen: an einer bewussten
Handlung und an einer aktiven Sitzung. Ein Client, dessen Benutzer das Banner
wegklickt oder der auf dem Login-Screen stehen bleibt, wird beliebig alt — und
der Client ist die Hälfte eines Sync-Protokolls, dessen andere Hälfte der
Server ständig weiterzieht. Alte Clients sind kein kosmetisches, sondern ein
Kompatibilitätsproblem.

Der Signaturpfad, der eine Automatik überhaupt erst verantwortbar macht, steht
bereits: Manifest und Artefakte kommen aus der Release-Verwaltung
([ADR-0035](0035-admin-release-management.md)), der Tauri-Updater verifiziert
gegen den eingebetteten minisign-Pubkey, und der Update-Feed ist öffentlich —
er braucht keine Sitzung.

## Entscheidung

**Jeder App-Start prüft und installiert, unabhängig vom Anmeldezustand.**
Findet der Check eine neuere Version, lädt er sie herunter, installiert sie und
startet die App neu — ohne Rückfrage, ohne Schalter. Der Endpunkt kommt aus der
persistierten `server_url` der letzten Paarung.

**Die Update-Frage wird vor allem anderen geklärt.** Zwei Dinge warten auf ihre
Antwort:

- *Die UI.* Solange die Stage nicht `Idle` ist, liegt ein undurchsichtiges
  Vollbild-Gate (`UpdateGate`) über der App — Login-Screen wie Dashboard sind
  dahinter gemountet, aber nicht sichtbar. Das Gate zeigt, was läuft: Suche,
  Download mit Fortschrittsbalken, Installation. Ein Overlay statt einer
  Verzweigung, damit das Login-Formular behält, was schon eingetippt ist.
- *Die Sync-Engine.* Sie startet erst, wenn das Signal aus `start_auto_update`
  kommt. Gegen einen Server zu synchronisieren, für den der Client gleich
  ersetzt wird, ist verworfene Arbeit, und der erste Walk würde mit dem
  Download um dieselbe Leitung konkurrieren.

**Ausnahme frische Installation.** Ohne persistierte `server_url` gibt es
nichts, wogegen geprüft werden könnte. Die Stage bleibt `Idle`, der
Login-Screen mit dem Server-Feld kommt sofort. Sobald die eingetippte URL
verifiziert (`verify_instance`), startet dieselbe Automatik gegen diese URL und
das Gate legt sich über das Formular. Die Serverauswahl steht damit vor dem
Login, die Update-Meldung zwischen beiden.

Der 6h-Poll bleibt ausdrücklich beim reinen Melden. Ein Prozess, der seit
Stunden läuft, darf sich nicht unter den Händen des Benutzers neu starten;
direkt nach dem Start kostet ein Neustart dagegen nichts.

Der manuelle „Jetzt prüfen"/„Installieren"-Weg in den Einstellungen bleibt
unverändert bestehen; er ist jetzt der Rückfallpfad, wenn die Automatik
scheitert.

## Konsequenzen

- `updater.rs` ist in `fetch_update` (reines I/O), `record_find` (Tracker,
  Event, Tray, optionale Benachrichtigung) und `install_with_progress`
  zerlegt. Der Startpfad `start_auto_update` setzt sie anders zusammen als das
  Kommando `check_for_update`: er unterdrückt die „Update
  verfügbar"-Benachrichtigung, schickt seine eigene und veröffentlicht seinen
  Zustand als `UpdateStage` (`update_stage`-Event plus `get_update_stage` für
  Frontends, die erst später zuhören).
- `start_polling` ist damit nur noch der Sitzungs-Poll und bekommt ein
  `immediate`-Flag: `true` bei frischem Login und nach 2FA, `false` beim
  Auto-Login im `setup`, wo der Startpfad denselben Endpunkt gerade schon
  abgefragt hat.
- Ein `AtomicBool` im `UpdateTracker` lässt immer nur einen Lauf zu — der
  Login-Screen kann die Automatik pro verifizierter URL erneut auslösen.
- Jeder Fehler — Server nicht erreichbar, Download- oder Installationsfehler —
  ist nicht-fatal und wird nur geloggt. Die Stage geht auf `Idle`, das Gate
  verschwindet, der Client läuft auf der alten Version weiter; Banner und
  Tray-Eintrag stehen aus `record_find` bereits.
- Der Start kostet jetzt eine Netzwerk-Rundreise, bevor irgendetwas sichtbar
  wird. Das Grace-Delay des Startpfads ist dafür entfallen — nur der
  Sitzungs-Poll wartet noch zwei Sekunden.
- Ein fehlerhaftes Release erreicht die Flotte jetzt ohne menschlichen
  Zwischenschritt. Das Release-Gate ([ADR-0007](0007-security-audit-release-gate.md))
  und ein sauberer Publish sind damit die einzige verbleibende Kontrolle.
- Kein neuer Config-Key, keine neue UI-Einstellung, kein Opt-out. Wer eine
  Version festhalten will, muss den Client vom Update-Endpunkt trennen.
