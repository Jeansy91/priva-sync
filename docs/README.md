# Dokumentation

Einstiegspunkt in die Projektdoku von PrivaSync. Die Doku ist deutsch, Code-Kommentare
sind englisch.

| Dokument | Inhalt |
| --- | --- |
| [`../README.md`](../README.md) | Projektüberblick, Quick start, Installation (englisch) |
| [`deployment.md`](deployment.md) | Betrieb: Docker Compose, Konfiguration, Reverse-Proxy, WebDAV, Office-Vorschau |
| [`../SPEC.md`](../SPEC.md) | Stabile Design-Spezifikation (Ziele, API, Sicherheit, Deployment) |
| [`architecture.md`](architecture.md) | Architekturüberblick: Komponenten, Datenfluss, Konsistenzmodell |
| [`glossary.md`](glossary.md) | Domänenbegriffe pro Bereich (Server, Desktop, Web) |
| [`development.md`](development.md) | Entwicklungsumgebung, Befehle, Tests, Testabdeckung, Konventionen |
| [`releasing.md`](releasing.md) | Release-Ablauf mit `scripts/bump-version.sh` und CI, Signing & Trust |
| [`adr/README.md`](adr/README.md) | Index aller Architecture Decision Records |

## Architekturentscheidungen (ADRs)

Begründungen für Design-Entscheidungen stehen als ADRs unter [`adr/`](adr/); den
vollständigen Index mit Titeln und Superseded-Verweisen findest du in
[`adr/README.md`](adr/README.md). Neue Entscheidung heißt neue ADR; eine überholte ADR
wird mit `Status: Superseded by ADR-XXXX` markiert und nicht umgeschrieben.

## Issues

Offene Arbeit steht im GitHub-Tracker des Projekts
(<https://github.com/Jeansy91/priva-sync>).
