import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import AuditLogPage from './AuditLogPage'
import { apiClient } from '../../store/auth'
import type { AdminUser } from '@privasync/api-client'

const ALICE: AdminUser = {
  id: 'u-alice', username: 'alice', email: 'a@x', role: 'user',
  quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
  totp_enabled: false, is_active: true, is_locked: false, avatar_url: null,
}

beforeEach(() => {
  vi.spyOn(apiClient.admin, 'listAuditLog' as any).mockResolvedValue({ events: [], count: 0 })
  vi.spyOn(apiClient.admin, 'listUsers').mockResolvedValue([])
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('AuditLogPage — DE', () => {
  it('rendert Header und leeren Zustand', async () => {
    const { findByRole, findByText } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Audit-Log' })).toBeInTheDocument()
    expect(await findByText('Keine Audit-Einträge gefunden.')).toBeInTheDocument()
  })

  it('ruft beim Mount apiClient.admin.listAuditLog auf', async () => {
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    renderWithI18n(<AuditLogPage />, { lang: 'de' })
    await vi.waitFor(() => expect(spy).toHaveBeenCalled())
  })
})

describe('AuditLogPage — Tabelle', () => {
  it('rendert Events mit übersetztem Event-Typ und IP', async () => {
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    spy.mockResolvedValueOnce({
      events: [{
        id: 'e1', user_id: null, event_type: 'auth.login.success',
        ip_address: '10.0.0.5', user_agent: 'curl/8', details: null,
        created_at: '2026-05-13T08:30:00Z',
      }],
      count: 1,
    })
    const { findByText } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    expect(await findByText('Login erfolgreich')).toBeInTheDocument()
    expect(await findByText('10.0.0.5')).toBeInTheDocument()
  })
})

describe('AuditLogPage — Filter', () => {
  it('Event-Auswahl ruft listAuditLog mit event=… auf', async () => {
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    const user = userEvent.setup()
    const { findByText, findAllByText } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    await user.click(await findByText('Alle Ereignisse'))
    // Dropdown-Option (es gibt parallel den Tabellen-Header — also alle treffen und letztes nehmen)
    const opts = await findAllByText('Login erfolgreich')
    await user.click(opts[opts.length - 1])
    await vi.waitFor(() => {
      const last = spy.mock.calls[spy.mock.calls.length - 1]?.[0]
      expect(last?.event).toBe('auth.login.success')
    })
  })

  it('Benutzer-Auswahl ruft listAuditLog mit user=<id> auf', async () => {
    ;(apiClient.admin.listUsers as ReturnType<typeof vi.fn>).mockResolvedValue([ALICE])
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    const user = userEvent.setup()
    const { findByText } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    await user.click(await findByText('Alle Benutzer'))
    await user.click(await findByText('alice'))
    await vi.waitFor(() => {
      const last = spy.mock.calls[spy.mock.calls.length - 1]?.[0]
      expect(last?.user).toBe('u-alice')
    })
  })
})

describe('AuditLogPage — Datumsfilter', () => {
  it('Von/Bis sind beim Mount sinnvoll vorgefüllt (letzte 7 Tage)', async () => {
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    const { findByLabelText } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    const from = await findByLabelText('Von') as HTMLInputElement
    const to   = await findByLabelText('Bis') as HTMLInputElement
    expect(from.value).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    expect(to.value).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    const diffDays = (new Date(to.value).getTime() - new Date(from.value).getTime()) / 86400000
    // From = 00:00 vor 7 Tagen, To = jetzt → Spanne 7.0 bis 8.0 Tage je nach Tageszeit.
    expect(diffDays).toBeGreaterThanOrEqual(6.99)
    expect(diffDays).toBeLessThanOrEqual(8.01)
    await vi.waitFor(() => {
      const last = spy.mock.calls[spy.mock.calls.length - 1]?.[0]
      expect(last?.from).toBeTruthy()
      expect(last?.to).toBeTruthy()
      expect(new Date(last.from).toISOString()).toBe(last.from)
    })
  })

  it('Von-Eingabe überschreibt Vorbelegung und löst API-Call aus', async () => {
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    const userE = userEvent.setup()
    const { findByLabelText } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    const from = await findByLabelText('Von') as HTMLInputElement
    await userE.clear(from)
    await userE.type(from, '2026-05-01T00:00')
    await vi.waitFor(() => {
      const last = spy.mock.calls[spy.mock.calls.length - 1]?.[0]
      expect(last?.from).toBe(new Date('2026-05-01T00:00').toISOString())
    })
  })
})

describe('AuditLogPage — Pagination', () => {
  it('zeigt "Weitere laden" wenn 100 Events kamen und lädt nächste Seite', async () => {
    const spy = apiClient.admin.listAuditLog as unknown as ReturnType<typeof vi.fn>
    const fullPage = Array.from({ length: 100 }, (_, i) => ({
      id: `e${i}`, user_id: null, event_type: 'auth.login.success',
      ip_address: '10.0.0.1', user_agent: '', details: null,
      created_at: '2026-05-13T00:00:00Z',
    }))
    spy.mockResolvedValueOnce({ events: fullPage, count: 100 })
    spy.mockResolvedValueOnce({ events: [], count: 0 })
    const user = userEvent.setup()
    const { findByRole } = renderWithI18n(<AuditLogPage />, { lang: 'de' })
    const btn = await findByRole('button', { name: 'Weitere laden' })
    await user.click(btn)
    await vi.waitFor(() => {
      const last = spy.mock.calls[spy.mock.calls.length - 1]?.[0]
      expect(last?.offset).toBe(100)
    })
  })
})

describe('AuditLogPage — EN', () => {
  it('renders header and empty state', async () => {
    const { findByRole, findByText } = renderWithI18n(<AuditLogPage />, { lang: 'en' })
    expect(await findByRole('heading', { level: 1, name: 'Audit log' })).toBeInTheDocument()
    expect(await findByText('No audit entries found.')).toBeInTheDocument()
  })
})
