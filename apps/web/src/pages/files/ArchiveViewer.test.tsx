import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { ArchiveViewer } from './ArchiveViewer'
import { ArchiveViewerDialog } from './ArchiveViewerDialog'
import { apiClient } from '../../store/auth'
import type { FileSource } from './fileSource'

const rootSrc: FileSource = { kind: 'root', rootID: 'r-1' }

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('ArchiveViewer', () => {
  it('zeigt einen Ladeindikator, bevor die Liste eintrifft', () => {
    vi.spyOn(apiClient.files, 'archiveList').mockReturnValue(new Promise(() => {}))
    const { getByText } = renderWithI18n(
      <ArchiveViewer source={rootSrc} path="/a.zip" name="a.zip" />,
      { lang: 'de' },
    )
    expect(getByText(/Archiv wird gelesen/i)).toBeInTheDocument()
  })

  it('listet Eintraege alphabetisch sortiert mit Groesse', async () => {
    vi.spyOn(apiClient.files, 'archiveList').mockResolvedValue({
      entries: [
        { name: 'zeta.txt', size: 2048, is_dir: false },
        { name: 'alpha', size: 0, is_dir: true },
      ],
    } as any)

    const { findByText, getByText } = renderWithI18n(
      <ArchiveViewer source={rootSrc} path="/a.zip" name="a.zip" />,
      { lang: 'de' },
    )

    await findByText('alpha')
    expect(getByText('zeta.txt')).toBeInTheDocument()
    expect(getByText('2 KB')).toBeInTheDocument()
    expect(getByText('—')).toBeInTheDocument()
    expect(getByText(/2 .*Eintr/i)).toBeInTheDocument()
  })

  it('zeigt eine Fehlermeldung, wenn das Laden fehlschlaegt', async () => {
    vi.spyOn(apiClient.files, 'archiveList').mockRejectedValue(new Error('boom'))
    const { findByText } = renderWithI18n(
      <ArchiveViewer source={rootSrc} path="/a.zip" name="a.zip" />,
      { lang: 'de' },
    )
    await findByText(/Archiv konnte nicht gelesen werden/i)
  })

  it('laedt ueber den Shared-Mount-Endpoint fuer Mount-Quellen', async () => {
    const spy = vi.spyOn(apiClient.sharedMounts, 'archiveList').mockResolvedValue({
      entries: [],
    } as any)
    const mountSrc: FileSource = { kind: 'mount', mountID: 'm-1' }
    renderWithI18n(
      <ArchiveViewer source={mountSrc} path="/x.zip" name="x.zip" />,
      { lang: 'de' },
    )
    await vi.waitFor(() => expect(spy).toHaveBeenCalledWith('m-1', '/x.zip'))
  })
})

describe('ArchiveViewerDialog', () => {
  it('rendert Namen, Inhalt und schliesst ueber den Close-Button', async () => {
    vi.spyOn(apiClient.files, 'archiveList').mockResolvedValue({
      entries: [{ name: 'readme.md', size: 12, is_dir: false }],
    } as any)
    const onClose = vi.fn()
    const { findByText, getByRole } = renderWithI18n(
      <ArchiveViewerDialog source={rootSrc} path="/a.zip" name="a.zip" onClose={onClose} />,
      { lang: 'de' },
    )
    await findByText('readme.md')
    fireEvent.click(getByRole('button'))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
