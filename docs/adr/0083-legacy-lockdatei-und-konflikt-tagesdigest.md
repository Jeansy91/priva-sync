# ADR-0083: Legacy-Dateien als Jet-Datenbanken sperren, Konflikt-Mails als Tagesdigest

**Status:** Accepted
**Datum:** 2026-11-11

## Kontext

Legacy Family Tree speichert Familiendatei (`<Name>.fdb`) und Verlauf
(`<Name>~hdb.hdb`) als Jet4-Datenbanken, hält sie im Betrieb in place offen und
legt daneben `<Name>.ldb` bzw. `<Name>~hdb.ldb` an. Die Ruheprüfung aus
ADR-0077 kannte nur die Access-Endungen; die Legacy-Datei wurde dadurch mehrfach
täglich mit je ~1,1 GB hochgeladen und konnte inkonsistent ankommen. Die
Konfliktpfade `26leg1231~hdb.conflict-….ldb` vom 1.10. belegen den Marker im
Sync-Verzeichnis.

Außerdem erzeugte jeder Konflikt eine eigene Mail. Der Digest aus dbf7f41 faltet
nur Zeilen zusammen, die beim Versand gleichzeitig anstehen; über den Tag
verteilte Konflikte ergaben weiterhin viele einzelne Mails.

## Entscheidung

1. **Legacy zählt zur Jet-Familie.** `fdb` und `hdb` kommen zu den
   Jet-Datenbank-Endungen in `snapshot.rs`. Existiert daneben die gleichnamige
   `.ldb`, wird die Datei wie bei Access zurückgestellt (`in_use`, ADR-0077).
2. **Konflikt-Mails einmal täglich.** Der Server legt die Outbox-Zeile mit
   `next_attempt_at` = nächste 08:00 Uhr (Serverzeit) an. Der bestehende Handler
   faltet beim Versand alle offenen Konflikt-Zeilen des Nutzers in eine Mail.

## Verworfen

- **`.~BM` als Marker:** Nicht belegt. Die Datei ist ungefähr so groß wie die
  `.fdb` und wirkt wie eine Sitzungskopie; ein dokumentierter Zusammenhang mit
  dem Öffnen existiert nicht. Der belegte Marker ist `.ldb`.
- **Digest per Cron-Job:** Zweiter Zustandsträger neben der Outbox; Retry und
  Atomarität mit der Konflikt-Zeile gehen verloren.
- **Konfigurierbare Uhrzeit pro Nutzer:** Kein Bedarf, nachrüstbar.

## Konsequenzen

- Eine offene Legacy-Datei wird erst nach dem Schließen hochgeladen.
- Konflikte werden bis zu 24 h später gemeldet; in der Weboberfläche und im
  Desktop-Client sind sie sofort sichtbar.
