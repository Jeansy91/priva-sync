# ADR-0084: Konfliktvergleich zeigt exakte Größe, Datum und Bildvorschau

**Status:** Accepted
**Datum:** 2026-11-11

## Kontext

Der Auflösen-Dialog (ADR-0003) zeigte im Web gerundete Größen und für die
Konfliktkopie nur den Erkennungszeitpunkt, im Desktop-Client gar keine
Angaben; für Binärdateien gab es nur den Hinweis „kein Textvergleich“. Bei
Fotodubletten, dem häufigsten Konflikt, lässt sich so nicht entscheiden.
Außerdem kannte der Server die Änderungszeit der Konfliktkopie nicht, sodass
ein Datumsvergleich nicht möglich war.

## Entscheidung

1. **Server speichert die mtime der Konfliktkopie** (`conflicts.loser_mtime`,
   Migration 041) und liefert sie als `conflict.mtime`. Quelle: mtime des
   Uploads, bei Move/Restore die der betroffenen Zeile. Ältere Konflikte haben
   `NULL`.
2. **Eine gemeinsame Komponente** `ConflictVersionCompare` (`packages/ui`) für
   Web und Desktop: je Version exakte Bytes (lokalisiert) plus gerundete Größe
   und Datum mit Sekunden. Abweichende Werte sind hervorgehoben. Stimmen Größe
   und Datum (auf die Sekunde) überein, steht ein Hinweis auf wahrscheinlich
   identischen Inhalt. Die Regeln liegen als reine Funktionen in
   `lib/conflictCompare.ts`.
3. **Fehlt die mtime der Konfliktkopie**, zeigt der Dialog den Erkennungszeitpunkt
   unter eigener Beschriftung und vergleicht ihn nicht mit dem Änderungsdatum.
4. **Bildvorschau** für jpg, jpeg, png, gif, webp, bmp und avif bis 25 MiB je
   Seite, nebeneinander mit Pixelmaßen. Web lädt beide Versionen als Blob,
   Desktop liest sie über den Befehl `conflict_preview` als `data:`-URL.
   TIFF und RAW bleiben ohne Vorschau, weil Webviews sie nicht darstellen.
   Der Desktop-Befehl `conflict_versions` liefert Größe und mtime beider Seiten
   direkt vom Dateisystem.

## Verworfen

- **Asset-Protokoll von Tauri für die Vorschau:** Bräuchte einen dynamischen
  Dateizugriffs-Scope über alle Sync-Roots; `data:`-URLs sind begrenzt und
  ohne Scope-Erweiterung auskömmlich.
- **Pixelgenauer Bildvergleich (Overlay/Differenz):** Mehr Aufwand ohne
  belegten Bedarf; Größe, Datum und Nebeneinander reichen für die Dubletten.
- **Datum der Konfliktkopie aus dem Erkennungszeitpunkt ableiten:** Äpfel mit
  Birnen, würde falsche Unterschiede markieren.

## Konsequenzen

- Konflikte vor Migration 041 zeigen für die Konfliktkopie nur den
  Erkennungszeitpunkt.
- Eine Vorschau lädt beide Bilder vollständig (bis 25 MiB je Seite).
