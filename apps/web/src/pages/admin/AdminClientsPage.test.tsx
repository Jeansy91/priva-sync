import { describe, it, expect, beforeEach, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import AdminClientsPage from './AdminClientsPage'
import { apiClient } from '../../store/auth'
import type { AdminClient } from '@privasync/api-client'

const SAMPLE: AdminClient[] = [
  {
    id: 'c1', user_id: 'u1', username: 'alice',
    device_id: 'dev-1', name: 'ALICE-PC', platform: 'windows',
    os_version: '11.0.26220', client_version: '0.4.2',
    last_ip: '10.0.0.7', last_seen: '2026-05-18T10:00:00Z',
    created_at: '2026-05-01T00:00:00Z',
  },
  {
    id: 'c2', user_id: 'u2', username: 'bob',
    device_id: 'dev-2', name: 'BOB-MAC', platform: 'macos',
    os_version: '14.4.1', client_version: '0.4.0',
    last_ip: null, last_seen: null,
    created_at: '2026-05-01T00:00:00Z',
  },
]

beforeEach(() => {
  vi.spyOn(apiClient.admin, 'listClients' as any).mockResolvedValue({ clients: SAMPLE })
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('AdminClientsPage', () => {
  it('rendert Header und Spalten auf deutsch', async () => {
    const { findByRole, findByText } = renderWithI18n(<AdminClientsPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Geräte' })).toBeInTheDocument()
    expect(await findByText('Benutzer')).toBeInTheDocument()
    expect(await findByText('Gerätename')).toBeInTheDocument()
    expect(await findByText('OS-Version')).toBeInTheDocument()
    expect(await findByText('Letzter Zugriff')).toBeInTheDocument()
  })

  it('zeigt eine Zeile pro Client mit Benutzername, Gerät und Versionen', async () => {
    const { findByText } = renderWithI18n(<AdminClientsPage />, { lang: 'de' })
    expect(await findByText('alice')).toBeInTheDocument()
    expect(await findByText('ALICE-PC')).toBeInTheDocument()
    expect(await findByText('11.0.26220')).toBeInTheDocument()
    expect(await findByText('0.4.2')).toBeInTheDocument()
    expect(await findByText('10.0.0.7')).toBeInTheDocument()
    expect(await findByText('bob')).toBeInTheDocument()
    expect(await findByText('BOB-MAC')).toBeInTheDocument()
  })

  it('zeigt "Nie" wenn last_seen fehlt', async () => {
    const { findAllByText } = renderWithI18n(<AdminClientsPage />, { lang: 'de' })
    expect((await findAllByText('Nie')).length).toBeGreaterThan(0)
  })

  it('zeigt lokalisierte Plattform-Labels', async () => {
    const { findByText } = renderWithI18n(<AdminClientsPage />, { lang: 'de' })
    expect(await findByText('Windows')).toBeInTheDocument()
    expect(await findByText('macOS')).toBeInTheDocument()
  })

  it('zeigt Empty-State wenn keine Clients existieren', async () => {
    vi.spyOn(apiClient.admin, 'listClients' as any).mockResolvedValueOnce({ clients: [] })
    const { findByText } = renderWithI18n(<AdminClientsPage />, { lang: 'de' })
    expect(await findByText('Noch keine Geräte angemeldet.')).toBeInTheDocument()
  })

  it('entfernt ein Gerät erst nach Bestätigung und lädt danach neu', async () => {
    const del = vi.spyOn(apiClient.admin, 'deleteClient' as any).mockResolvedValue(undefined)
    const list = apiClient.admin.listClients as unknown as ReturnType<typeof vi.fn>
    const { findByRole, getByRole } = renderWithI18n(<AdminClientsPage />, { lang: 'de' })

    await findByRole('button', { name: 'Gerät ALICE-PC entfernen' })
    // The spy is shared across this file's tests, so only the delta counts.
    const listCallsBefore = list.mock.calls.length

    await userEvent.click(getByRole('button', { name: 'Gerät ALICE-PC entfernen' }))
    expect(del).not.toHaveBeenCalled()

    await userEvent.click(getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('c1'))
    // The list is re-fetched so a row deleted elsewhere cannot linger.
    await waitFor(() => expect(list.mock.calls.length).toBe(listCallsBefore + 1))
  })
})
