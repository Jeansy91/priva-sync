import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../test/renderWithI18n'
import AdminPage from './AdminPage'
import { apiClient, useAuthStore } from '../store/auth'
import type { AdminUser } from '@privasync/api-client'

function makeUser(over: Partial<AdminUser> = {}): AdminUser {
  return {
    id: 'u-1', username: 'alice', email: 'a@x', role: 'user',
    quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
    totp_enabled: false, is_active: true, is_locked: false, avatar_url: null,
    ...over,
  }
}

beforeEach(() => {
  vi.spyOn(apiClient.admin, 'listUsers').mockResolvedValue([])
  vi.spyOn(console, 'error').mockImplementation(() => {})
  useAuthStore.setState({
    user: { id: 'admin-self', username: 'root', email: 'r@x', role: 'admin',
      totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
      avatar_url: null, preferred_language: 'de' },
  } as any)
})

describe('AdminPage — DE', () => {
  it('rendert Header und Empty-State', async () => {
    const { findByRole, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Benutzerverwaltung' })).toBeInTheDocument()
    expect(await findByText('Keine Benutzer gefunden.')).toBeInTheDocument()
  })
})

describe('AdminPage — EN', () => {
  it('renders header and empty state', async () => {
    const { findByRole, findByText } = renderWithI18n(<AdminPage />, { lang: 'en' })
    expect(await findByRole('heading', { level: 1, name: 'User management' })).toBeInTheDocument()
    expect(await findByText('No users found.')).toBeInTheDocument()
  })
})

describe('AdminPage — Rolle (via Aktionen-Menü)', () => {
  async function openMenu(u: ReturnType<typeof userEvent.setup>, findByRole: any) {
    const trigger = await findByRole('button', { name: 'Aktionen' })
    await u.click(trigger)
  }

  it('Menü-Eintrag "Zum Admin machen" ruft setRole("admin")', async () => {
    const alice = makeUser({ role: 'user' })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([alice])
    const setRole = vi.spyOn(apiClient.admin, 'setRole').mockResolvedValue(undefined as any)
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    await u.click(await findByRole('menuitem', { name: /Zum Admin/ }))
    await vi.waitFor(() => expect(setRole).toHaveBeenCalledWith('u-1', 'admin'))
  })

  it('Demote-Eintrag bei eigenem User ist deaktiviert', async () => {
    const self = makeUser({ id: 'admin-self', username: 'root', role: 'admin' })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([self])
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    const item = await findByRole('menuitem', { name: /Admin entziehen/ })
    expect(item).toBeDisabled()
  })

  it('Lock-Eintrag bei eigenem aktiven User ist deaktiviert', async () => {
    const self = makeUser({ id: 'admin-self', username: 'root', role: 'admin', is_active: true })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([self])
    const u = userEvent.setup()
    const { findByRole } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    const item = await findByRole('menuitem', { name: /^Sperren$/ })
    expect(item).toBeDisabled()
  })

  it('Entsperren-Eintrag ruft unlockUser und zeigt eine Erfolgsmeldung', async () => {
    const bob = makeUser({ id: 'u-2', username: 'bob', is_locked: true })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([bob])
    const unlockUser = vi.spyOn(apiClient.admin, 'unlockUser').mockResolvedValue(undefined as never)
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    await u.click(await findByRole('menuitem', { name: /Login-Sperre aufheben/ }))

    await vi.waitFor(() => expect(unlockUser).toHaveBeenCalledWith('u-2'))
    expect(await findByText('Login-Sperre für bob wurde aufgehoben.')).toBeInTheDocument()
  })

  it('Neu-berechnen-Eintrag ruft recomputeQuota und aktualisiert die Anzeige', async () => {
    const bob = makeUser({ id: 'u-2', username: 'bob', used_bytes: 10, trash_bytes: 1 })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([bob])
    vi.spyOn(apiClient.admin, 'recomputeQuota').mockResolvedValue({ used_bytes: 99, trash_bytes: 3 } as never)
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    await u.click(await findByRole('menuitem', { name: /Speicher neu berechnen/ }))

    expect(await findByText('Speicherverbrauch für bob neu berechnet.')).toBeInTheDocument()
  })

  it('Sperren-Eintrag ruft setActive(false) fuer einen fremden aktiven User', async () => {
    const bob = makeUser({ id: 'u-2', username: 'bob', is_active: true })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([bob])
    const setActive = vi.spyOn(apiClient.admin, 'setActive').mockResolvedValue(undefined as never)
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    await u.click(await findByRole('menuitem', { name: /^Sperren$/ }))

    await vi.waitFor(() => expect(setActive).toHaveBeenCalledWith('u-2', false))
    expect(await findByText('bob wurde gesperrt.')).toBeInTheDocument()
  })

  it('zeigt eine Fehlermeldung, wenn eine Aktion fehlschlaegt', async () => {
    const bob = makeUser({ id: 'u-2', username: 'bob', is_locked: true })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([bob])
    vi.spyOn(apiClient.admin, 'unlockUser').mockRejectedValue({ body: { error: 'server down' } })
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })
    await openMenu(u, findByRole)
    await u.click(await findByRole('menuitem', { name: /Login-Sperre aufheben/ }))

    expect(await findByText('server down')).toBeInTheDocument()
  })
})

describe('AdminPage — nicht-leere Tabelle', () => {
  it('rendert mehrere Zeilen und aktualisiert die Untertitel-Anzahl', async () => {
    const users = [makeUser({ id: 'u-1', username: 'alice' }), makeUser({ id: 'u-2', username: 'bob' })]
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue(users)
    const { findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })

    expect(await findByText('2 Benutzer registriert')).toBeInTheDocument()
    expect(await findByText('alice')).toBeInTheDocument()
    expect(await findByText('bob')).toBeInTheDocument()
  })

  it('sortiert die Tabelle nach Klick auf die Spaltenkopf', async () => {
    const users = [makeUser({ id: 'u-1', username: 'zoe' }), makeUser({ id: 'u-2', username: 'alice' })]
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue(users)
    const u = userEvent.setup()
    const { findByRole, findAllByRole } = renderWithI18n(<AdminPage />, { lang: 'de' })

    // The sortable click target is the <button> inside the <th>, not the
    // <th>/columnheader itself -- clicking the th doesn't bubble into it.
    // Exact match is required: a loose /Benutzer/ regex also matches the
    // always-present "Benutzer anlegen" create button, which findByRole
    // resolves to first (before the table has even loaded), clicking that
    // instead of waiting for the sortable header.
    await u.click(await findByRole('button', { name: 'Benutzer' }))

    const rows = await findAllByRole('row')
    // First row is the header — the first data row should now be "alice".
    expect(rows[1]).toHaveTextContent('alice')
  })
})

describe('AdminPage — Dialoge', () => {
  it('legt einen Benutzer an und zeigt die Erfolgsmeldung', async () => {
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([])
    vi.spyOn(apiClient.admin, 'createUser').mockResolvedValue(makeUser({ id: 'u-9', username: 'newbie' }))
    const u = userEvent.setup()
    const { findByRole, findByLabelText, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })

    await u.click(await findByRole('button', { name: 'Benutzer anlegen' }))
    await u.type(await findByLabelText('Benutzername'), 'newbie')
    await u.type(await findByLabelText('E-Mail'), 'newbie@example.com')
    await u.click(await findByRole('button', { name: 'Erstellen' }))

    expect(await findByText(/newbie.*angelegt/)).toBeInTheDocument()
    expect(await findByText('newbie')).toBeInTheDocument()
  })

  it('setzt ein Kontingent und zeigt die Erfolgsmeldung', async () => {
    const bob = makeUser({ id: 'u-2', username: 'bob' })
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([bob])
    vi.spyOn(apiClient.admin, 'setQuota').mockResolvedValue(undefined as never)
    const u = userEvent.setup()
    const { findByRole, findByLabelText, findByText } = renderWithI18n(<AdminPage />, { lang: 'de' })

    await openMenu(u, findByRole)
    await u.click(await findByRole('menuitem', { name: /Kontingent setzen/ }))
    const quotaInput = await findByLabelText('Kontingent (GB)')
    await u.clear(quotaInput)
    await u.type(quotaInput, '5')
    await u.click(await findByRole('button', { name: 'Speichern' }))

    expect(await findByText('Kontingent wurde aktualisiert.')).toBeInTheDocument()
    expect(apiClient.admin.setQuota).toHaveBeenCalledWith('u-2', 5 * 1024 * 1024 * 1024)
  })

  async function openMenu(u: ReturnType<typeof userEvent.setup>, findByRole: any) {
    const trigger = await findByRole('button', { name: 'Aktionen' })
    await u.click(trigger)
  }
})
