import { describe, it, expect, vi } from 'vitest'
import { fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { apiClient } from '../../store/auth'
import { FileDetailPanel, type DetailSelection } from './FileDetailPanel'
import type { FileEntry, SharedMount } from '@privasync/api-client'

function fileEntry(over: Partial<FileEntry> = {}): FileEntry {
  return {
    id: 'f-1',
    path: '/Dokumente/Bericht.pdf',
    size: 2_500_000,
    mtime: '2026-04-01T10:30:00Z',
    content_hash: 'abc',
    is_deleted: false,
    updated_at: '2026-04-02T08:00:00Z',
    ...over,
  }
}

function fileSel(over: Partial<FileEntry> = {}): DetailSelection {
  return { kind: 'file', file: fileEntry(over) }
}

const aliceOwner = { id: 'u-alice', username: 'alice', avatar_url: null }

function dirSel(over: Partial<Extract<DetailSelection, { kind: 'dir' }>> = {}): DetailSelection {
  return {
    kind:        'dir',
    name:        'Bilder',
    path:        '/Dokumente/Bilder',
    itemCount:   12,
    totalSize:   5_400_000,
    latestMtime: '2026-03-15T08:00:00Z',
    ...over,
  }
}

describe('FileDetailPanel', () => {
  it('listet Zugriffsberechtigte als Avatare, getrennt schreibend/lesend (ADR-0015)', async () => {
    vi.spyOn(apiClient.shares, 'access').mockResolvedValue({
      writers: [{ id: 'u-bob', username: 'bob', avatar_url: null }],
      readers: [{ id: 'u-carol', username: 'carol', avatar_url: null }],
    })
    const { findByText, getByLabelText } = renderWithI18n(
      <FileDetailPanel
        rootID="r-1"
        selection={fileSel()}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    expect(await findByText('bob')).toBeInTheDocument()
    expect(await findByText('carol')).toBeInTheDocument()
    getByLabelText('Schreibend')
    getByLabelText('Lesend')
  })

  it('zeigt Leerzustand-Hinweis, wenn keine Auswahl', () => {
    const { getByText } = renderWithI18n(
      <FileDetailPanel
        selection={null}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    expect(getByText(/Wähle eine Datei aus/i)).toBeTruthy()
  })

  it('zeigt Datei-Felder bei Datei-Auswahl', () => {
    const { getByText, getByLabelText } = renderWithI18n(
      <FileDetailPanel
        selection={fileSel()}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    expect(getByText('Bericht.pdf')).toBeTruthy()
    expect(getByLabelText(/Größe/i).textContent).toMatch(/2[.,]\d+\s*MB/i)
    expect(getByLabelText(/Pfad/i).textContent).toContain('/Dokumente/Bericht.pdf')
    expect(getByLabelText(/MIME/i).textContent).toContain('application/pdf')
    expect(getByLabelText(/Geändert/i).textContent).toMatch(/2026/)
  })

  it('ruft onShare und onDownload bei Klick (Datei)', () => {
    const onShare = vi.fn()
    const onDownload = vi.fn()
    const { getByRole } = renderWithI18n(
      <FileDetailPanel
        selection={fileSel()}
        onShare={onShare}
        onDownload={onDownload}
        onPreview={() => {}}
      />,
    )
    fireEvent.click(getByRole('button', { name: /Teilen/i }))
    fireEvent.click(getByRole('button', { name: /Herunterladen/i }))
    expect(onShare).toHaveBeenCalledTimes(1)
    expect(onDownload).toHaveBeenCalledTimes(1)
  })

  it('zeigt Eigentümer mit Initial-Avatar bei Datei-Auswahl', () => {
    const { getByLabelText } = renderWithI18n(
      <FileDetailPanel
        selection={fileSel()}
        owner={aliceOwner}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    const ownerRow = getByLabelText(/Eigentümer/i)
    expect(ownerRow.textContent).toContain('alice')
    // Avatar-Fallback rendert die ersten zwei Buchstaben des Usernamens.
    expect(ownerRow.textContent).toContain('AL')
  })

  it('zeigt Eigentümer auch bei Verzeichnis-Auswahl', () => {
    const { getByLabelText } = renderWithI18n(
      <FileDetailPanel
        selection={dirSel()}
        owner={aliceOwner}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    expect(getByLabelText(/Eigentümer/i).textContent).toContain('alice')
  })

  it('rendert eine Vorschau für Datei-Mounts ohne rootID über den Mount-Endpoint', async () => {
    const thumb = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok:   false,
      status: 415,
    } as any)
    const mount: SharedMount = {
      id: 'm-1', name: 'cat.jpg', path: '/Photos/cat.jpg', is_file: true,
      can_write: false, expires_at: null,
      owner: { id: 'u-a', username: 'alice', avatar_url: null },
    }
    const selection: DetailSelection = {
      kind: 'file', file: fileEntry({ path: '/Photos/cat.jpg' }), mount,
    }
    const { getByTestId } = renderWithI18n(
      <FileDetailPanel
        selection={selection}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    // Preview region present even though no rootID is set …
    expect(getByTestId('file-detail-preview')).toBeTruthy()
    // … and it fetched through the Shared-Mount endpoint with sub-path "/".
    await waitFor(() =>
      expect(thumb).toHaveBeenCalledWith('m-1', '/', 256, 'contain'),
    )
  })

  it('rendert Vorschau für Datei IM Mount-Verzeichnis über den Mount-Endpoint', async () => {
    // Browsing inside a dir-mount: selection has no .mount decoration and the
    // rootID is the share facade id. mountMode must route the preview through
    // the mount endpoint with the file's mount-relative path — not the
    // sync-root endpoint, which would treat the share id as a sync-root id.
    const thumb = vi.spyOn(apiClient.sharedMounts, 'thumbnail').mockResolvedValue({
      ok: false, status: 415,
    } as any)
    const rootThumb = vi.spyOn(apiClient.files, 'thumbnail')

    const { getByTestId } = renderWithI18n(
      <FileDetailPanel
        rootID="s-1"
        mountMode
        selection={fileSel({ path: '/Reports/cat.jpg' })}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    expect(getByTestId('file-detail-preview')).toBeTruthy()
    await waitFor(() =>
      expect(thumb).toHaveBeenCalledWith('s-1', '/Reports/cat.jpg', 256, 'contain'),
    )
    expect(rootThumb).not.toHaveBeenCalled()
  })

  it('zeigt Schreibzugriff als Icon + Klartext, nicht als ja/nein', () => {
    const mount: SharedMount = {
      id: 'm-1', name: 'cat.jpg', path: '/Photos/cat.jpg', is_file: true,
      can_write: true, expires_at: null,
      owner: { id: 'u-a', username: 'alice', avatar_url: null },
    }
    const { getByLabelText } = renderWithI18n(
      <FileDetailPanel
        selection={{ kind: 'file', file: fileEntry({ path: '/Photos/cat.jpg' }), mount }}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    const access = getByLabelText(/Zugriff/i)
    expect(access.textContent).toContain('Lesen & Schreiben')
    expect(access.textContent).not.toMatch(/\bja\b/i)
    // Icon + Klartext: die Zeile trägt ein Status-Icon.
    expect(access.querySelector('svg')).not.toBeNull()
  })

  it('zeigt Nur-Lese-Mount als "Nur Lesen", nicht als ja/nein', () => {
    const mount: SharedMount = {
      id: 'm-2', name: 'cat.jpg', path: '/Photos/cat.jpg', is_file: true,
      can_write: false, expires_at: null,
      owner: { id: 'u-a', username: 'alice', avatar_url: null },
    }
    const { getByLabelText } = renderWithI18n(
      <FileDetailPanel
        selection={{ kind: 'file', file: fileEntry({ path: '/Photos/cat.jpg' }), mount }}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    const access = getByLabelText(/Zugriff/i)
    expect(access.textContent).toContain('Nur Lesen')
    expect(access.textContent).not.toMatch(/\bnein\b/i)
    expect(access.querySelector('svg')).not.toBeNull()
  })

  it('blendet die Ablauf-Zeile aus, wenn kein Ablaufdatum gesetzt ist', () => {
    const base = {
      id: 'm-3', name: 'cat.jpg', path: '/Photos/cat.jpg', is_file: true,
      can_write: false, owner: { id: 'u-a', username: 'alice', avatar_url: null },
    }
    const render = (expires_at: string | null) => renderWithI18n(
      <FileDetailPanel
        selection={{ kind: 'file', file: fileEntry({ path: '/Photos/cat.jpg' }), mount: { ...base, expires_at } as SharedMount }}
        onShare={() => {}} onDownload={() => {}} onPreview={() => {}}
      />,
    )

    const { queryByText, unmount } = render(null)
    expect(queryByText('Ablauf')).toBeNull()
    unmount()

    const { getByText } = render('2026-12-31T00:00:00Z')
    expect(getByText('Ablauf')).toBeInTheDocument()
  })

  it('zeigt Verzeichnis-Felder bei Verzeichnis-Auswahl', () => {
    const { getByText, getByLabelText, queryByRole } = renderWithI18n(
      <FileDetailPanel
        selection={dirSel()}
        onShare={() => {}}
        onDownload={() => {}}
        onPreview={() => {}}
      />,
    )
    expect(getByText('Bilder')).toBeTruthy()
    expect(getByLabelText(/Typ/i).textContent).toMatch(/Verzeichnis/i)
    expect(getByLabelText(/Einträge/i).textContent).toContain('12')
    expect(getByLabelText(/Größe/i).textContent).toMatch(/MB/i)
    expect(getByLabelText(/Pfad/i).textContent).toContain('/Dokumente/Bilder')
    // ADR-0012: Verzeichnis bekommt jetzt einen Herunterladen-Button (ZIP via Token).
    expect(queryByRole('button', { name: /Herunterladen/i })).not.toBeNull()
  })
})
