import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderWithI18n } from '../../test/renderWithI18n'
import { ModelViewerDialog } from './ModelViewerDialog'
import { apiClient } from '../../store/auth'
import type { FileSource } from './fileSource'

const rootSrc: FileSource = { kind: 'root', rootID: 'r-1' }

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('ModelViewerDialog', () => {
  it('zeigt einen Ladeindikator, bevor das Blob eintrifft', () => {
    vi.spyOn(apiClient.files, 'download').mockReturnValue(new Promise(() => {}))
    const { getByLabelText } = renderWithI18n(
      <ModelViewerDialog source={rootSrc} path="/m.stl" name="m.stl" onClose={() => {}} />,
      { lang: 'de' },
    )
    // aria-label carries the dialog title while the model is still loading.
    expect(getByLabelText('Vorschau: m.stl')).toBeInTheDocument()
  })

  it('zeigt eine Fehlermeldung, wenn der Download fehlschlaegt', async () => {
    vi.spyOn(apiClient.files, 'download').mockResolvedValue({ ok: false } as any)
    const { findByText } = renderWithI18n(
      <ModelViewerDialog source={rootSrc} path="/m.stl" name="m.stl" onClose={() => {}} />,
      { lang: 'de' },
    )
    await findByText(/3D-Modell konnte nicht geladen werden|Modell konnte nicht geladen werden/i)
  })

  it('laedt ueber den Shared-Mount-Endpoint fuer Mount-Quellen', async () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'download').mockResolvedValue({ ok: false } as any)
    const mountSrc: FileSource = { kind: 'mount', mountID: 'm-1' }
    renderWithI18n(
      <ModelViewerDialog source={mountSrc} path="/" name="m.glb" onClose={() => {}} />,
      { lang: 'de' },
    )
    await vi.waitFor(() => expect(spy).toHaveBeenCalledWith('m-1', '/'))
  })
})
