import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { AppShell } from './AppShell'
import { apiClient, useAuthStore } from '../../store/auth'
import type { Me } from '@privasync/api-client'

vi.mock('../../hooks/useSSE', () => ({
  useSSE: () => {},
}))

const navigateMock = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return { ...actual, useNavigate: () => navigateMock }
})

function baseUser(overrides: Partial<Me> = {}): Me {
  return {
    id: 'u-1', username: 'alice', email: 'a@b.de', role: 'user',
    totp_enabled: false, quota_bytes: 1000, used_bytes: 400, trash_bytes: 0,
    avatar_url: null, preferred_language: 'de', show_file_tree: true,
    ...overrides,
  } as unknown as Me
}

function setUser(user: Me | null) {
  useAuthStore.setState({ user, accessToken: 't', isAuthenticated: true })
}

beforeEach(() => {
  vi.restoreAllMocks()
  navigateMock.mockClear()
  vi.spyOn(apiClient.auth, 'logout').mockResolvedValue(undefined as any)
  vi.spyOn(apiClient.auth, 'fetchAvatar').mockResolvedValue(null as any)
  setUser(baseUser())
})

describe('AppShell — Navigation', () => {
  it('zeigt Standard-Nav-Punkte, aber keine Admin-Punkte fuer normale User', () => {
    const { getAllByText, queryByText } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    expect(getAllByText('Dateien').length).toBeGreaterThan(0)
    expect(getAllByText('Freigaben').length).toBeGreaterThan(0)
    expect(getAllByText('Konflikte').length).toBeGreaterThan(0)
    expect(getAllByText('Papierkorb').length).toBeGreaterThan(0)
    expect(getAllByText('Einstellungen').length).toBeGreaterThan(0)
    expect(queryByText('Verwaltung')).not.toBeInTheDocument()
    expect(queryByText('Benutzerverwaltung')).not.toBeInTheDocument()
  })

  it('zeigt die Admin-Sektion aufklappbar fuer Admin-User', () => {
    setUser(baseUser({ role: 'admin' }))
    const { getAllByText, getAllByRole } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    const sectionButtons = getAllByRole('button', { name: 'Verwaltung' })
    expect(sectionButtons.length).toBeGreaterThan(0)
    // containsActive = false for /files, so section starts collapsed.
    expect(sectionButtons[0]).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(sectionButtons[0])
    expect(sectionButtons[0]).toHaveAttribute('aria-expanded', 'true')
    expect(getAllByText('Benutzerverwaltung').length).toBeGreaterThan(0)
  })

  it('startet die Admin-Sektion offen, wenn die aktuelle Route darin liegt', () => {
    setUser(baseUser({ role: 'admin' }))
    const { getAllByRole } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/admin/users' },
    )
    const sectionButtons = getAllByRole('button', { name: 'Verwaltung' })
    expect(sectionButtons[0]).toHaveAttribute('aria-expanded', 'true')
  })

  it('persistiert den Auf/Zu-Zustand der Admin-Sektion in localStorage', () => {
    localStorage.clear()
    setUser(baseUser({ role: 'admin' }))
    const { getAllByRole } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    const btn = getAllByRole('button', { name: 'Verwaltung' })[0]
    fireEvent.click(btn)
    expect(localStorage.getItem('appShell.section.admin.nav.section')).toBe('1')
    fireEvent.click(btn)
    expect(localStorage.getItem('appShell.section.admin.nav.section')).toBe('0')
  })
})

describe('AppShell — Quota-Anzeige', () => {
  it('zeigt den Quota-Prozentsatz basierend auf used+trash Bytes', () => {
    setUser(baseUser({ quota_bytes: 1000, used_bytes: 400, trash_bytes: 100 }))
    const { getAllByText } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    // (400 + 100) / 1000 = 50 %
    expect(getAllByText('50%').length).toBeGreaterThan(0)
  })

  it('zeigt keine Quota-Bar, wenn quota_bytes 0 ist', () => {
    setUser(baseUser({ quota_bytes: 0 }))
    const { queryByText } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    expect(queryByText('Speicher')).not.toBeInTheDocument()
  })

  it('zeigt die Papierkorb-Groesse, wenn trashBytes > 0', () => {
    setUser(baseUser({ quota_bytes: 1000, used_bytes: 0, trash_bytes: 2048 }))
    const { getAllByText } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    expect(getAllByText(/2(\.0)? KB/).length).toBeGreaterThan(0)
  })
})

describe('AppShell — User & Logout', () => {
  it('zeigt Initialen, solange kein Avatar geladen ist', () => {
    setUser(baseUser({ username: 'alicesmith', avatar_url: null }))
    const { getAllByText } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    expect(getAllByText('AL').length).toBeGreaterThan(0)
  })

  it('meldet ab, ruft die API, leert den Store und navigiert zu /login', async () => {
    const logoutSpy = vi.spyOn(apiClient.auth, 'logout').mockResolvedValue(undefined as any)
    const { getAllByTitle, findByTitle } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    const btn = getAllByTitle('Abmelden')[0]
    fireEvent.click(btn)
    await findByTitle('Abmelden')
    expect(logoutSpy).toHaveBeenCalledOnce()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
    expect(navigateMock).toHaveBeenCalledWith('/login')
  })
})

describe('AppShell — Mobile-Sidebar', () => {
  it('oeffnet und schliesst die mobile Sidebar per Menu/X-Button', () => {
    const { container, getAllByTitle } = renderWithI18n(
      <AppShell><div>content</div></AppShell>,
      { lang: 'de', route: '/files' },
    )
    // Only the (always-rendered, desktop) Sidebar's close button exists at first.
    expect(getAllByTitle('Schließen')).toHaveLength(1)

    const menuButton = container.querySelector('header button') as HTMLElement
    fireEvent.click(menuButton)
    // Mobile overlay mounts a second Sidebar with its own close button.
    expect(getAllByTitle('Schließen')).toHaveLength(2)

    fireEvent.click(getAllByTitle('Schließen')[1])
    expect(getAllByTitle('Schließen')).toHaveLength(1)
  })
})
