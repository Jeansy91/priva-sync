import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

const fetchMyAvatar = vi.fn()
const logout = vi.fn()
const getConfig = vi.fn()
const wipeAccountLocal = vi.fn()

vi.mock('../tauri', () => ({
  cmd: {
    fetchMyAvatar: (...a: unknown[]) => fetchMyAvatar(...a),
    logout: (...a: unknown[]) => logout(...a),
    getConfig: (...a: unknown[]) => getConfig(...a),
    wipeAccountLocal: (...a: unknown[]) => wipeAccountLocal(...a),
  },
}))

import AppHeader from './AppHeader'
import { useAppStore } from '../store'

const loggedInConfig = {
  is_logged_in: true,
  username: 'alice',
  server_url: 'https://sync.example.com',
} as any

beforeEach(() => {
  fetchMyAvatar.mockReset().mockResolvedValue(null)
  logout.mockReset().mockResolvedValue(undefined)
  getConfig.mockReset().mockResolvedValue(loggedInConfig)
  wipeAccountLocal.mockReset().mockResolvedValue(undefined)
  useAppStore.setState({ config: loggedInConfig, status: null, pathWarnings: [] })
})

describe('AppHeader', () => {
  it('renders the username and a shortened server URL', () => {
    renderWithI18n(<AppHeader page="sync" onNavigate={vi.fn()} onAfterWipe={vi.fn()} />)
    expect(screen.getByText('alice')).toBeInTheDocument()
    expect(screen.getByText('sync.example.com')).toBeInTheDocument()
  })

  it('navigates when a nav button is clicked', () => {
    const onNavigate = vi.fn()
    renderWithI18n(<AppHeader page="sync" onNavigate={onNavigate} onAfterWipe={vi.fn()} />)

    fireEvent.click(screen.getByTitle('Konflikte'))
    expect(onNavigate).toHaveBeenCalledWith('conflicts')
  })

  it('shows a badge with the combined conflict + warning count', () => {
    useAppStore.setState({
      config: loggedInConfig,
      status: { conflict_count: 2 } as any,
      pathWarnings: [{ path: '/a' } as any],
    })
    renderWithI18n(<AppHeader page="sync" onNavigate={vi.fn()} onAfterWipe={vi.fn()} />)
    expect(screen.getByText('3')).toBeInTheDocument()
  })

  it('logs out after confirming, refreshing config', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    renderWithI18n(<AppHeader page="sync" onNavigate={vi.fn()} onAfterWipe={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: /alice/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Abmelden' }))

    await waitFor(() => expect(logout).toHaveBeenCalled())
    expect(getConfig).toHaveBeenCalled()
  })

  it('does not log out when the confirm dialog is declined', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    renderWithI18n(<AppHeader page="sync" onNavigate={vi.fn()} onAfterWipe={vi.fn()} />)

    fireEvent.click(screen.getByRole('button', { name: /alice/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Abmelden' }))

    await new Promise((r) => setTimeout(r, 0))
    expect(logout).not.toHaveBeenCalled()
  })

  it('wipes the local account after confirming and calls onAfterWipe', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const onAfterWipe = vi.fn()
    renderWithI18n(<AppHeader page="sync" onNavigate={vi.fn()} onAfterWipe={onAfterWipe} />)

    fireEvent.click(screen.getByRole('button', { name: /alice/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Entfernen' }))

    await waitFor(() => expect(wipeAccountLocal).toHaveBeenCalled())
    await waitFor(() => expect(onAfterWipe).toHaveBeenCalled())
  })
})
