import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderWithI18n } from '../test/renderWithI18n'
import DownloadPage from './DownloadPage'

function stubUserAgent(ua: string, platform = '') {
  Object.defineProperty(window.navigator, 'userAgent', { value: ua, configurable: true })
  Object.defineProperty(window.navigator, 'platform', { value: platform, configurable: true })
}

function mockFetch(status: number, body?: unknown) {
  const fn = vi.fn().mockResolvedValue({
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

const WIN_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)'

const release = {
  version: '1.2.3',
  release_url: 'https://github.com/example/repo/releases/tag/v1.2.3',
  downloads: [
    { target: 'darwin-aarch64', file: 'a.dmg', url: 'https://example.test/a.dmg' },
    { target: 'darwin-x86_64', file: 'b.dmg', url: 'https://example.test/b.dmg' },
    { target: 'linux-x86_64', file: 'c.AppImage', url: 'https://example.test/c.AppImage' },
    { target: 'windows-x86_64', file: 'd-setup.exe', url: 'https://example.test/d-setup.exe' },
  ],
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('DownloadPage', () => {
  it('queries the public downloads endpoint', async () => {
    stubUserAgent(WIN_UA, 'Win32')
    const fetchFn = mockFetch(200, release)

    const { findByText } = renderWithI18n(<DownloadPage />)
    await findByText(/Version 1.2.3 herunterladen/)

    expect(fetchFn.mock.calls[0][0]).toBe('/api/v1/client/downloads')
  })

  it('shows the empty state when the server offers no downloads', async () => {
    stubUserAgent(WIN_UA, 'Win32')
    mockFetch(200, { version: '', release_url: '', downloads: [] })

    const { findByText } = renderWithI18n(<DownloadPage />)

    expect(await findByText('Dieser Server stellt noch keine Client-Downloads bereit.')).toBeInTheDocument()
  })

  it('shows an error message when the fetch fails', async () => {
    stubUserAgent(WIN_UA, 'Win32')
    mockFetch(500)

    const { findByText } = renderWithI18n(<DownloadPage />)

    expect(await findByText('Konnte Release-Informationen nicht laden.')).toBeInTheDocument()
  })

  it('highlights the detected platform and lists all others', async () => {
    stubUserAgent(WIN_UA, 'Win32')
    mockFetch(200, release)

    const { findByRole, getByRole, getAllByText } = renderWithI18n(<DownloadPage />)

    const primary = await findByRole('link', { name: /Version 1.2.3 herunterladen/ })
    expect(primary).toHaveAttribute('href', 'https://example.test/d-setup.exe')
    expect(getByRole('link', { name: 'macOS · Apple Silicon' })).toHaveAttribute('href', 'https://example.test/a.dmg')
    expect(getByRole('link', { name: 'macOS · Intel' })).toHaveAttribute('href', 'https://example.test/b.dmg')
    expect(getByRole('link', { name: 'Linux · x86_64' })).toHaveAttribute('href', 'https://example.test/c.AppImage')
    expect(getAllByText(/SmartScreen/).length).toBeGreaterThan(0)
  })

  it('links to the release notes on GitHub', async () => {
    stubUserAgent(WIN_UA, 'Win32')
    mockFetch(200, release)

    const { findByRole } = renderWithI18n(<DownloadPage />)

    const link = await findByRole('link', { name: 'Versionshinweise auf GitHub' })
    expect(link).toHaveAttribute('href', release.release_url)
  })

  it('renders English texts', async () => {
    stubUserAgent(WIN_UA, 'Win32')
    mockFetch(200, release)

    const { findByText } = renderWithI18n(<DownloadPage />, { lang: 'en' })

    expect(await findByText('Release notes on GitHub')).toBeInTheDocument()
  })
})
