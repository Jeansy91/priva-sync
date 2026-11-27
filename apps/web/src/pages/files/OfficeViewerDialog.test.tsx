import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderWithI18n } from '../../test/renderWithI18n'
import { OfficeViewerDialog } from './OfficeViewerDialog'
import { apiClient } from '../../store/auth'
import { ApiRequestError } from '@privasync/api-client'
import type { FileSource } from './fileSource'

const rootSrc: FileSource  = { kind: 'root',  rootID: 'r-1' }
const mountSrc: FileSource = { kind: 'mount', mountID: 'm-1' }

const docEditorCtor = vi.fn().mockImplementation(() => ({ destroyEditor: vi.fn() }))

beforeEach(() => {
  vi.restoreAllMocks()
  docEditorCtor.mockClear()
  // Simulate the document server's own DocsAPI script already having loaded —
  // the actual <script> injection/loading is a browser concern out of scope
  // for jsdom; see loadDocsApi's early-return branch in OfficeViewerDialog.
  window.DocsAPI = { DocEditor: docEditorCtor }
})

afterEach(() => {
  delete window.DocsAPI
  // loadDocsApi appends <script> tags directly to document.head, outside
  // the React tree — testing-library's cleanup() never touches those, so
  // without this they'd leak across tests/files and shadow each other.
  document.querySelectorAll('script[src*="/web-apps/apps/api/documents/api.js"]')
    .forEach((el) => el.remove())
})

describe('OfficeViewerDialog (ADR-0027)', () => {
  it('instanziiert DocsAPI.DocEditor mit der signierten Konfiguration', async () => {
    vi.spyOn(apiClient.files, 'officeConfig').mockResolvedValue({
      document_server_url: 'https://office.example.com',
      config: { document: { key: 'abc' } },
    })

    renderWithI18n(
      <OfficeViewerDialog source={rootSrc} path="/report.docx" name="report.docx" onClose={() => {}} />,
      { lang: 'de' },
    )

    await vi.waitFor(() => {
      expect(docEditorCtor).toHaveBeenCalledTimes(1)
    })
    expect(docEditorCtor.mock.calls[0][1]).toEqual({ document: { key: 'abc' } })
  })

  it('laedt Datei-Mounts ueber den Shared-Mount-Endpoint', async () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'officeConfig').mockResolvedValue({
      document_server_url: 'https://office.example.com',
      config: {},
    })

    renderWithI18n(
      <OfficeViewerDialog source={mountSrc} path="/sheet.xlsx" name="sheet.xlsx" onClose={() => {}} />,
      { lang: 'de' },
    )

    await vi.waitFor(() => expect(docEditorCtor).toHaveBeenCalledTimes(1))
    expect(spy).toHaveBeenCalledWith('m-1', '/sheet.xlsx')
  })

  it('faellt bei 404 (Document-Server nicht konfiguriert) auf die Nicht-unterstuetzt-Meldung zurueck', async () => {
    vi.spyOn(apiClient.files, 'officeConfig').mockRejectedValue(
      new ApiRequestError(404, { error: 'not found' }),
    )

    const { findByText } = renderWithI18n(
      <OfficeViewerDialog source={rootSrc} path="/report.docx" name="report.docx" onClose={() => {}} />,
      { lang: 'de' },
    )

    await findByText(/Für dieses Format ist keine Vorschau verfügbar/i)
    expect(docEditorCtor).not.toHaveBeenCalled()
  })

  // Reviewer-reported regressions: without window.DocsAPI pre-set, loadDocsApi
  // actually injects and waits on a <script> tag — the two scenarios below
  // exercise exactly that path via manual dispatch of load/error events,
  // since jsdom never fetches/executes the script itself.
  describe('Skript-Laden ohne bereits gesetztes DocsAPI-Global', () => {
    beforeEach(() => {
      delete window.DocsAPI
    })

    it('zeigt die Fallback-Meldung, wenn das Skript laedt aber kein DocsAPI-Global setzt (z. B. HTML-Fehlerseite)', async () => {
      vi.spyOn(apiClient.files, 'officeConfig').mockResolvedValue({
        document_server_url: 'https://office.example.com',
        config: { document: { key: 'abc' } },
      })

      const { findByText } = renderWithI18n(
        <OfficeViewerDialog source={rootSrc} path="/report.docx" name="report.docx" onClose={() => {}} />,
        { lang: 'de' },
      )

      const script = await vi.waitFor(() => {
        const el = document.querySelector<HTMLScriptElement>(
          'script[src="https://office.example.com/web-apps/apps/api/documents/api.js"]',
        )
        if (!el) throw new Error('script tag not appended yet')
        return el
      })
      // window.DocsAPI intentionally stays undefined here, simulating a
      // reverse proxy returning HTTP 200 with a non-DocsAPI body.
      script.dispatchEvent(new Event('load'))

      await findByText(/Für dieses Format ist keine Vorschau verfügbar/i)
      expect(docEditorCtor).not.toHaveBeenCalled()
    })

    it('haengt nicht dauerhaft im Ladezustand, wenn dieselbe Datei nach einem fehlgeschlagenen Laden erneut geoeffnet wird', async () => {
      vi.spyOn(apiClient.files, 'officeConfig').mockResolvedValue({
        document_server_url: 'https://office.example.com',
        config: { document: { key: 'abc' } },
      })

      const first = renderWithI18n(
        <OfficeViewerDialog source={rootSrc} path="/report.docx" name="report.docx" onClose={() => {}} />,
        { lang: 'de' },
      )
      const firstScript = await vi.waitFor(() => {
        const el = document.querySelector<HTMLScriptElement>(
          'script[src="https://office.example.com/web-apps/apps/api/documents/api.js"]',
        )
        if (!el) throw new Error('script tag not appended yet')
        return el
      })
      firstScript.dispatchEvent(new Event('error'))
      await first.findByText(/Für dieses Format ist keine Vorschau verfügbar/i)
      first.unmount()

      // Second open of the same file — must not reuse the now-dead first
      // <script> tag's already-fired events and hang on the spinner forever.
      renderWithI18n(
        <OfficeViewerDialog source={rootSrc} path="/report.docx" name="report.docx" onClose={() => {}} />,
        { lang: 'de' },
      )
      const scripts = await vi.waitFor(() => {
        const els = document.querySelectorAll<HTMLScriptElement>(
          'script[src="https://office.example.com/web-apps/apps/api/documents/api.js"]',
        )
        if (els.length < 2) throw new Error('second script tag not appended yet')
        return els
      })
      window.DocsAPI = { DocEditor: docEditorCtor }
      scripts[scripts.length - 1].dispatchEvent(new Event('load'))

      await vi.waitFor(() => expect(docEditorCtor).toHaveBeenCalledTimes(1))
    })
  })
})
