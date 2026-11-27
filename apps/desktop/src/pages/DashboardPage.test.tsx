import { describe, it, expect, vi } from 'vitest'
import { act, screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

let tauriListener: ((e: { payload: string }) => void) | null = null

vi.mock('@tauri-apps/api/event', () => ({
  listen: (_event: string, cb: (e: { payload: string }) => void) => {
    tauriListener = cb
    return Promise.resolve(() => {})
  },
}))

vi.mock('../components/SyncRootsTab', () => ({ default: () => <div>sync-tab</div> }))
vi.mock('../components/ConflictsTab', () => ({ default: () => <div>conflicts-tab</div> }))
vi.mock('../components/SettingsTab', () => ({ default: () => <div>settings-tab</div> }))
vi.mock('../components/TransferSpeedBar', () => ({ default: () => <div>speed-bar</div> }))
vi.mock('../components/AppHeader', () => ({
  default: ({ onNavigate }: { onNavigate: (p: string) => void }) => (
    <div>
      <button onClick={() => onNavigate('conflicts')}>go-conflicts</button>
      <button onClick={() => onNavigate('settings')}>go-settings</button>
    </div>
  ),
}))

import DashboardPage from './DashboardPage'

describe('DashboardPage', () => {
  it('shows the sync tab by default', () => {
    renderWithI18n(<DashboardPage />)
    expect(screen.getByText('sync-tab')).toBeInTheDocument()
    expect(screen.queryByText('conflicts-tab')).not.toBeInTheDocument()
  })

  it('switches tabs when AppHeader navigates', () => {
    renderWithI18n(<DashboardPage />)

    fireEvent.click(screen.getByText('go-conflicts'))
    expect(screen.getByText('conflicts-tab')).toBeInTheDocument()

    fireEvent.click(screen.getByText('go-settings'))
    expect(screen.getByText('settings-tab')).toBeInTheDocument()
  })

  it('switches to the page named by a tray_navigate event', async () => {
    renderWithI18n(<DashboardPage />)
    await waitFor(() => expect(tauriListener).not.toBeNull())

    act(() => tauriListener!({ payload: 'conflicts' }))

    await waitFor(() => expect(screen.getByText('conflicts-tab')).toBeInTheDocument())
  })

  it('ignores an unknown tray_navigate payload', async () => {
    renderWithI18n(<DashboardPage />)
    await waitFor(() => expect(tauriListener).not.toBeNull())

    act(() => tauriListener!({ payload: 'unknown-page' }))

    expect(screen.getByText('sync-tab')).toBeInTheDocument()
  })
})
