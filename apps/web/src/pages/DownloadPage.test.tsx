import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderWithI18n } from '../test/renderWithI18n'
import DownloadPage from './DownloadPage'

function stubUserAgent(ua: string, platform = '') {
  Object.defineProperty(window.navigator, 'userAgent', { value: ua, configurable: true })
  Object.defineProperty(window.navigator, 'platform', { value: platform, configurable: true })
}

function mockFetchManifest(status: number, body?: unknown) {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  }))
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('DownloadPage', () => {
  it('shows the empty state when no release is available (204)', async () => {
    stubUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Win32')
    mockFetchManifest(204)

    const { findByText } = renderWithI18n(<DownloadPage />)

    expect(await findByText('Es steht noch kein Release bereit.')).toBeInTheDocument()
  })

  it('shows an error message when the manifest fetch fails', async () => {
    stubUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Win32')
    mockFetchManifest(500)

    const { findByText } = renderWithI18n(<DownloadPage />)

    expect(await findByText('Konnte Release-Informationen nicht laden.')).toBeInTheDocument()
  })

  it('shows the recommended download for the detected platform', async () => {
    stubUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Win32')
    mockFetchManifest(200, {
      version: '1.2.3',
      platforms: {
        'windows-x86_64': { signature: 'sig', url: 'https://example.test/app.msi' },
        'linux-x86_64': { signature: 'sig', url: 'https://example.test/app.AppImage' },
      },
    })

    const { findByText, getByText } = renderWithI18n(<DownloadPage />)

    expect(await findByText(/Version 1.2.3 herunterladen/)).toBeInTheDocument()
    expect(getByText('Windows · x86_64')).toBeInTheDocument()
    expect(getByText('Linux · x86_64')).toBeInTheDocument()
  })

  it('links to the GitHub release page of the offered version instead of showing notes', async () => {
    stubUserAgent('Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 'Win32')
    mockFetchManifest(200, {
      version: '1.2.3',
      platforms: {
        'windows-x86_64': { signature: 'sig', url: 'https://example.test/app.msi' },
      },
    })

    const { findByRole } = renderWithI18n(<DownloadPage />)

    const link = await findByRole('link', { name: 'Versionshinweise auf GitHub' })
    expect(link).toHaveAttribute('href', 'https://github.com/Jeansy91/priva-sync/releases/tag/v1.2.3')
  })
})
