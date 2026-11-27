import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderWithI18n } from '../../test/renderWithI18n'
import AdminStatsPage from './AdminStatsPage'
import { apiClient } from '../../store/auth'
import type { AdminStats } from '@privasync/api-client'

const SAMPLE: AdminStats = {
  version: '0.2.1',
  users:        { total: 7, active: 6, admin: 2, totp: 3, locked: 1 },
  sync_roots:   { total: 12 },
  files:        { live: 1234, trash: 56, conflicts: 2 },
  shares:       { total: 9, active: 7, password_protected: 4 },
  storage:      { used_bytes: 5_000_000, trash_bytes: 1_000_000, quota_bytes: 100_000_000, disk_free_bytes: 50_000_000_000, disk_total_bytes: 200_000_000_000 },
  quota_health: { warning80: 1, critical95: 0 },
  activity_24h: { logins: 42, failed_logins: 3 },
  health:       { outbox_pending: 0, db_size_bytes: 25_000_000, uptime_seconds: 7200 },
}

beforeEach(() => {
  vi.spyOn(apiClient.admin, 'getStats' as any).mockResolvedValue(SAMPLE)
  vi.spyOn(console, 'error').mockImplementation(() => {})
})

describe('AdminStatsPage — DE', () => {
  it('rendert Header', async () => {
    const { findByRole } = renderWithI18n(<AdminStatsPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Statistiken' })).toBeInTheDocument()
  })

  it('zeigt Versionsnummer aus API', async () => {
    const { findByText } = renderWithI18n(<AdminStatsPage />, { lang: 'de' })
    expect(await findByText('0.2.1')).toBeInTheDocument()
  })

  it('zeigt eindeutige Counts und Bytes formatiert', async () => {
    const { findByText, findAllByText } = renderWithI18n(<AdminStatsPage />, { lang: 'de' })
    expect(await findByText('12')).toBeInTheDocument()        // sync roots
    expect(await findByText('1.234')).toBeInTheDocument()     // files.live, DE-locale tausender
    expect(await findByText('42')).toBeInTheDocument()        // 24h logins
    expect((await findAllByText('7')).length).toBeGreaterThan(0) // users.total bzw. shares.active
  })
})
