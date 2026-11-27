# ADR-0049: Office-Formatumfang und Round-Trip-Editierbarkeit

**Status:** Accepted
**Datum:** 2026-08-26

## Kontext

ADR-0027 hat den externen Document-Server ("Euro-Office") mit genau drei
Formaten in Betrieb genommen: DOCX, XLSX, PPTX. Der Server kann deutlich mehr —
sein API-Vertrag listet die vollständige Palette an Textdokumenten, Tabellen und
Präsentationen inklusive ODF, der Alt-Binärformate und der makrofähigen
OOXML-Varianten. Alles außerhalb der drei Endungen landete bisher in der
"Format nicht unterstützt"-Meldung, obwohl der Server es problemlos öffnet.

Mit ADR-0033 schreibt der Server bearbeitete Bytes zurück (Save-Back-Callback).
Das macht die Formatfrage zweiteilig: *öffnen können* und *unverändert
zurückschreiben können* sind nicht dasselbe. Der Document-Server konvertiert
beim Öffnen alles, was er nicht nativ bearbeitet, nach OOXML. Ein Save-Back
würde dann OOXML-Bytes unter der ursprünglichen Endung ablegen — eine `.doc`,
die in Wahrheit DOCX enthält; eine `.docm`, deren Makros stillschweigend
verschwunden sind.

## Entscheidung

**Der Formatumfang ist die Office-Palette des Document-Servers, ohne die
PDF-Familie.** `officedoc.extTypes` führt Textdokumente, Tabellen und
Präsentationen: OOXML (inkl. Vorlagen und Makro-Varianten), ODF (inkl. Vorlagen
und Flat-XML), die Alt-Binärformate, StarOffice/Kingsoft/iWork.

Bewusst ausgenommen:

- **PDF, DjVu, XPS, OXPS** — die eigene PDF-Vorschau bleibt zuständig.
- **Text- und Markup-Formate** (`txt`, `csv`, `tsv`, `md`, `xml`, `html`) — die
  App öffnet sie im eigenen Monaco-Editor, der sie *bearbeitbar* macht statt sie
  durch eine Konvertierung zu schicken.
- **EPUB** und Bildformate — eigene Viewer.
- **Visio/Diagramme** — kein stabiler `documentType` im API-Vertrag.

**Editierbarkeit ist eine Eigenschaft des Formats, nicht nur der Berechtigung.**
`officedoc.Type.Editable` ist genau für die Formate wahr, die der
Document-Server nativ bearbeitet *und* im selben Format zurückschreibt: OOXML
ohne Makros und ODF. Der Save-Back-Callback wird nur für diese Formate gemintet;
alle übrigen bekommen `mode: "view"`, unabhängig davon, ob die aufrufende
Person Schreibrecht hat.

Die effektive Editierbarkeit ist damit die Konjunktion aus Schreibrecht
(ADR-0033: eigener Root bzw. `can_write` des Mounts) und Formatfähigkeit.
Makrofähige OOXML-Formate (`docm`, `xlsm`, `pptm`, …) zählen bewusst als
view-only: der Server öffnet sie, verwirft die Makros aber beim Speichern.

## Konsequenzen

- Die Vorschau öffnet ODF-Dokumente (ODT/ODS/ODP) mit vollem Bearbeiten und
  Save-Back — der praktisch wichtigste Zugewinn.
- Alt-Formate sind sichtbar, aber nicht bearbeitbar. Wer eine `.doc` ändern
  will, konvertiert sie vorher; das ist die ehrlichere Variante als ein Save,
  der die Datei heimlich umformatiert.
- `previewKind.ts` (Web) spiegelt `officedoc.extTypes` (Server). Die beiden
  Listen müssen zusammen gepflegt werden — der Server lehnt eine nicht
  gelistete Endung mit `unsupported file type` ab, die UI würde sonst einen
  Viewer öffnen, der sofort in die Fehlermeldung läuft.
- Ein Format, das die konkrete Euro-Office-Installation doch nicht kann,
  scheitert weich: `officeConfigFrom` schlägt fehl, der Dialog zeigt dieselbe
  "nicht unterstützt"-Meldung wie zuvor.
