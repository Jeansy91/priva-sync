import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'
import SharesPage from './SharesPage'
import { apiClient } from '../store/auth'
import type { PathShareInfo, SyncRoot } from '@privasync/api-client'

const sseState = vi.hoisted(() => ({ handlers: {} as Record<string, (d: unknown) => void> }))
vi.mock('../hooks/useSSE', () => ({
  useSSE: (h: Record<string, (d: unknown) => void>) => { sseState.handlers = h },
}))

const root: SyncRoot = { id: 'r-1', name: 'home', path: '/', created_at: '' } as unknown as SyncRoot

const userGrant = (over: Partial<PathShareInfo> = {}): PathShareInfo => ({
  id: 'grant-1', share_id: 'agg-1', share_type: 'user',
  target_user_id: 'u-2', target_username: 'bob', can_write: false, expires_at: null, ...over,
})

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
  vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({})
  // Default: an addable user exists so the "add person" option is shown.
  vi.spyOn(apiClient.users, 'search').mockResolvedValue([{ id: 'u-free', username: 'dana' }])
  vi.spyOn(console, 'error').mockImplementation(() => {})
  sseState.handlers = {}
})

describe('SharesPage — header & empty state', () => {
  it('rendert Header und Empty-State (DE)', async () => {
    const { findByRole, findByText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Freigaben' })).toBeInTheDocument()
    expect(await findByText('Noch keine Freigaben')).toBeInTheDocument()
  })

  it('renders header and empty state (EN)', async () => {
    const { findByRole, findByText } = renderWithI18n(<SharesPage />, { lang: 'en' })
    expect(await findByRole('heading', { level: 1, name: 'Shares' })).toBeInTheDocument()
    expect(await findByText('No shares yet')).toBeInTheDocument()
  })
})

describe('SharesPage — grouped shares', () => {
  it('zeigt Empfänger und beide öffentlichen Links (Verzeichnis)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({
      '/Docs': [
        userGrant(),
        { id: 'pub-1', share_id: 'pub-1', share_type: 'public', can_write: false,
          expires_at: null, read_token: 'tokr', write_token: 'tokw' },
      ],
    })
    const { findByText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    expect(await findByText('bob')).toBeInTheDocument()
    expect(await findByText(/\/s\/tokr$/)).toBeInTheDocument()
    expect(await findByText(/\/s\/tokw$/)).toBeInTheDocument()
  })

  it('entfernt einen Empfänger (Controls immer sichtbar)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({ '/Docs': [userGrant()] })
    const removeSpy = vi.spyOn(apiClient.shares, 'removeGrant').mockResolvedValue(undefined)

    const { findByText, getByLabelText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText('bob')
    await act(async () => { fireEvent.click(getByLabelText('Entfernen')) })
    await waitFor(() => expect(removeSpy).toHaveBeenCalledWith('agg-1', 'u-2'))
  })

  it('blendet das Add-Formular hinter dem "Person hinzufügen"-Button ein', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({ '/Docs': [userGrant()] })

    const { findByText, getByText, getByPlaceholderText, queryByPlaceholderText } =
      renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText('bob')
    // Form is hidden until the add button is clicked.
    expect(queryByPlaceholderText(/Benutzername des Empfängers/i)).toBeNull()
    fireEvent.click(getByText('Person hinzufügen'))
    expect(getByPlaceholderText(/Benutzername des Empfängers/i)).toBeInTheDocument()
  })

  it('blendet bereits hinzugefügte Empfänger aus dem Picker aus', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({ '/Docs': [userGrant()] })
    // Search returns bob (already granted, u-2) and alice (u-3).
    vi.spyOn(apiClient.users, 'search').mockResolvedValue([
      { id: 'u-2', username: 'bob' },
      { id: 'u-3', username: 'alice' },
    ])

    const { findAllByText, getByText, getByPlaceholderText, queryAllByText } =
      renderWithI18n(<SharesPage />, { lang: 'de' })
    await findAllByText('bob') // grant row
    fireEvent.click(getByText('Person hinzufügen'))
    await act(async () => { fireEvent.focus(getByPlaceholderText(/Benutzername des Empfängers/i)) })

    expect(await findAllByText('alice')).not.toHaveLength(0)
    // bob only appears as the existing grant row, never as a picker option.
    expect(queryAllByText('bob')).toHaveLength(1)
  })

  it('bearbeitet einen Empfänger über ein Modal (Stift → updateGrant)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({ '/Docs': [userGrant()] })
    const updateSpy = vi.spyOn(apiClient.shares, 'updateGrant').mockResolvedValue({
      id: 'agg-1', sync_root_id: 'r-1', path: '/Docs', share_type: 'user',
      token: null, expires_at: null, can_write: true, created_at: new Date().toISOString(),
    })

    const { findByText, getByLabelText, getByText, getByRole } = renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText('bob')
    fireEvent.click(getByLabelText('Bearbeiten'))
    // Modal opened.
    expect(getByText('Zugriff bearbeiten')).toBeInTheDocument()
    await act(async () => { fireEvent.click(getByRole('button', { name: 'Speichern' })) })
    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith('agg-1', 'u-2', expect.objectContaining({ can_write: false })))
  })

  it('blendet "Person hinzufügen" aus, wenn keine gültigen Benutzer übrig sind', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({ '/Docs': [userGrant()] })
    // The only user in the system is bob, who is already granted.
    vi.spyOn(apiClient.users, 'search').mockResolvedValue([{ id: 'u-2', username: 'bob' }])

    const { findByText, queryByText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText('bob')
    await waitFor(() => expect(queryByText('Person hinzufügen')).toBeNull())
  })
})

describe('SharesPage — Ablaufdatum öffentlicher Links', () => {
  it('erzeugt einen Lese-Link mit beim Erzeugen gewähltem Ablaufdatum', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({})
    const genSpy = vi.spyOn(apiClient.shares, 'createPublicLink').mockResolvedValue({
      id: 'pub-1', sync_root_id: 'r-1', path: '/New', share_type: 'public',
      token: null, read_token: 'tokr', expires_at: null,
      read_expires_at: new Date('2026-12-31T12:00').toISOString(),
      can_write: false, created_at: new Date().toISOString(),
    })

    const { findAllByRole, getAllByLabelText } = renderWithI18n(<SharesPage />, {
      lang: 'de', route: '/shares?root=r-1&path=' + encodeURIComponent('/New') + '&dir=1',
    })

    // Read row comes first; its picker and generate button share the index.
    const buttons = await findAllByRole('button', { name: 'Erzeugen' })
    await act(async () => {
      fireEvent.change(getAllByLabelText('Ablaufdatum')[0], { target: { value: '2026-12-31T12:00' } })
    })
    fireEvent.click(buttons[0])
    await waitFor(() => expect(genSpy).toHaveBeenCalledWith(
      'r-1', '/New', 'read', new Date('2026-12-31T12:00').toISOString(),
    ))
  })

  it('zeigt das Ablaufdatum direkt nach dem Erzeugen an (Reload-Pfad)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    const mapSpy = vi.spyOn(apiClient, 'sharesByRoot')
    mapSpy.mockResolvedValueOnce({}) // initial load: no shares yet
    mapSpy.mockResolvedValue({
      '/New': [
        { id: 'pub-1', share_id: 'pub-1', share_type: 'public', can_write: false,
          expires_at: null, read_token: 'tokr', write_token: null,
          read_expires_at: new Date('2026-12-31T12:00').toISOString() },
      ],
    })
    vi.spyOn(apiClient.shares, 'createPublicLink').mockResolvedValue({
      id: 'pub-1', sync_root_id: 'r-1', path: '/New', share_type: 'public',
      token: null, read_token: 'tokr', expires_at: null,
      read_expires_at: new Date('2026-12-31T12:00').toISOString(),
      can_write: false, created_at: new Date().toISOString(),
    })

    const { findAllByRole, findByText } = renderWithI18n(<SharesPage />, {
      lang: 'de', route: '/shares?root=r-1&path=' + encodeURIComponent('/New') + '&dir=1',
    })
    const buttons = await findAllByRole('button', { name: 'Erzeugen' })
    await act(async () => { fireEvent.click(buttons[0]) })
    expect(await findByText(/Läuft ab/)).toBeInTheDocument()
  })

  it('zeigt ein Alt-Limit (Aggregat-expires_at) am Link an', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({
      '/Docs': [
        { id: 'pub-1', share_id: 'pub-1', share_type: 'public', can_write: false,
          expires_at: new Date('2026-12-31T12:00').toISOString(),
          read_token: 'tokr', write_token: null },
      ],
    })

    const { findByText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText(/\/s\/tokr$/)
    expect(await findByText(/Läuft ab/)).toBeInTheDocument()
  })

  it('zeigt das Ablaufdatum eines bestehenden Links an', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({
      '/Docs': [
        { id: 'pub-1', share_id: 'pub-1', share_type: 'public', can_write: false,
          expires_at: null, read_token: 'tokr', write_token: null,
          read_expires_at: new Date('2026-12-31T12:00').toISOString() },
      ],
    })

    const { findByText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText(/\/s\/tokr$/)
    expect(await findByText(/Läuft ab/)).toBeInTheDocument()
  })
})

describe('SharesPage — Suchfilter', () => {
  it('filtert Freigaben nach Pfad/Empfänger', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({
      '/Apfel': [userGrant({ id: 'g-a', share_id: 'agg-a', target_user_id: 'u-a', target_username: 'alice' })],
      '/Zebra': [userGrant({ id: 'g-b', share_id: 'agg-b', target_user_id: 'u-b', target_username: 'bob' })],
    })

    const { findByText, getByPlaceholderText, queryByText } = renderWithI18n(<SharesPage />, { lang: 'de' })
    await findByText('alice')
    await act(async () => { fireEvent.change(getByPlaceholderText('Freigaben durchsuchen…'), { target: { value: 'zebra' } }) })
    expect(queryByText('alice')).toBeNull()
    expect(await findByText('bob')).toBeInTheDocument()
  })
})

describe('SharesPage — navigated target path', () => {
  it('öffnet eine leere Gruppe im Edit-Modus und erzeugt einen Lese-Link', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient, 'sharesByRoot').mockResolvedValue({})
    const genSpy = vi.spyOn(apiClient.shares, 'createPublicLink').mockResolvedValue({
      id: 'pub-1', sync_root_id: 'r-1', path: '/New', share_type: 'public',
      token: null, read_token: 'tokr', expires_at: null, can_write: false,
      created_at: new Date().toISOString(),
    })

    const { findAllByRole } = renderWithI18n(<SharesPage />, {
      lang: 'de', route: '/shares?root=r-1&path=' + encodeURIComponent('/New') + '&dir=1',
    })

    // Dir → read + write "Erzeugen" buttons; the first is the read link.
    const buttons = await findAllByRole('button', { name: 'Erzeugen' })
    fireEvent.click(buttons[0])
    await waitFor(() => expect(genSpy).toHaveBeenCalledWith('r-1', '/New', 'read', null))
  })
})
