import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import { apiClient, useAuthStore } from '../../store/auth'
import { AvatarSection } from './AvatarSection'

function setUser(over: Record<string, unknown> = {}) {
  useAuthStore.setState({
    user: {
      id: 'u-1', username: 'alice', email: 'a@x', role: 'user',
      totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
      avatar_url: null, preferred_language: 'de',
      ...over,
    },
  } as any)
}

function file(size: number, name = 'avatar.png') {
  const f = new File(['x'.repeat(Math.max(1, size))], name, { type: 'image/png' })
  Object.defineProperty(f, 'size', { value: size })
  return f
}

beforeEach(() => {
  vi.restoreAllMocks()
  setUser()
})

describe('AvatarSection — upload', () => {
  it('uploads a picked file and refreshes the user', async () => {
    vi.spyOn(apiClient.auth, 'uploadAvatar').mockResolvedValue({ avatar_url: '/a.png' })
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({
      id: 'u-1', username: 'alice', email: 'a@x', role: 'user',
      totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
      avatar_url: '/a.png', preferred_language: 'de',
    } as never)
    const u = userEvent.setup()
    const { container, findByText } = renderWithI18n(<AvatarSection />)
    const input = container.querySelector('input[type="file"]') as HTMLInputElement

    await u.upload(input, file(1000))

    expect(await findByText('Profilbild aktualisiert.')).toBeInTheDocument()
    expect(useAuthStore.getState().user?.avatar_url).toBe('/a.png')
  })

  it('rejects an oversized file client-side without calling the API', async () => {
    const uploadSpy = vi.spyOn(apiClient.auth, 'uploadAvatar')
    const u = userEvent.setup()
    const { container, findByText } = renderWithI18n(<AvatarSection />)
    const input = container.querySelector('input[type="file"]') as HTMLInputElement

    await u.upload(input, file(2 * 1024 * 1024))

    expect(await findByText('Datei zu groß — max. 1 MB.')).toBeInTheDocument()
    expect(uploadSpy).not.toHaveBeenCalled()
  })

  it('shows a 413 message when the server also rejects on size', async () => {
    vi.spyOn(apiClient.auth, 'uploadAvatar').mockRejectedValue({ status: 413 })
    const u = userEvent.setup()
    const { container, findByText } = renderWithI18n(<AvatarSection />)
    const input = container.querySelector('input[type="file"]') as HTMLInputElement

    await u.upload(input, file(100))

    expect(await findByText('Datei zu groß — max. 1 MB.')).toBeInTheDocument()
  })

  it('shows an invalid-image message on a 400', async () => {
    vi.spyOn(apiClient.auth, 'uploadAvatar').mockRejectedValue({ status: 400 })
    const u = userEvent.setup()
    const { container, findByText } = renderWithI18n(<AvatarSection />)
    const input = container.querySelector('input[type="file"]') as HTMLInputElement

    await u.upload(input, file(100))

    expect(await findByText('Ungültiges Bild — PNG oder JPG erforderlich.')).toBeInTheDocument()
  })

  it('shows a generic failure message on any other error', async () => {
    vi.spyOn(apiClient.auth, 'uploadAvatar').mockRejectedValue({ status: 500 })
    const u = userEvent.setup()
    const { container, findByText } = renderWithI18n(<AvatarSection />)
    const input = container.querySelector('input[type="file"]') as HTMLInputElement

    await u.upload(input, file(100))

    expect(await findByText('Upload fehlgeschlagen.')).toBeInTheDocument()
  })
})

describe('AvatarSection — delete', () => {
  it('shows the remove button only when an avatar is set, and deletes it', async () => {
    setUser({ avatar_url: '/a.png' })
    vi.spyOn(apiClient.auth, 'deleteAvatar').mockResolvedValue(undefined as never)
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({
      id: 'u-1', username: 'alice', email: 'a@x', role: 'user',
      totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
      avatar_url: null, preferred_language: 'de',
    } as never)
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<AvatarSection />)

    await u.click(await findByRole('button', { name: /Entfernen/i }))

    expect(await findByText('Profilbild entfernt.')).toBeInTheDocument()
    expect(useAuthStore.getState().user?.avatar_url).toBeNull()
  })

  it('has no remove button when there is no avatar', () => {
    const { queryByRole } = renderWithI18n(<AvatarSection />)
    expect(queryByRole('button', { name: /Entfernen/i })).not.toBeInTheDocument()
  })

  it('shows an error message when deletion fails', async () => {
    setUser({ avatar_url: '/a.png' })
    vi.spyOn(apiClient.auth, 'deleteAvatar').mockRejectedValue(new Error('fail'))
    const u = userEvent.setup()
    const { findByRole, findByText } = renderWithI18n(<AvatarSection />)

    await u.click(await findByRole('button', { name: /Entfernen/i }))

    expect(await findByText('Löschen fehlgeschlagen.')).toBeInTheDocument()
  })
})
