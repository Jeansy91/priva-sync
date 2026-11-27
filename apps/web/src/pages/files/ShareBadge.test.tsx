import { describe, it, expect } from 'vitest'
import type { PathShareInfo } from '@privasync/api-client'
import { renderWithI18n } from '../../test/renderWithI18n'
import { ShareBadge } from './ShareBadge'

function userShare(over: Partial<PathShareInfo> = {}): PathShareInfo {
  return {
    id:               's-1',
    share_id:         's-1',
    share_type:       'user',
    target_user_id:   'u-bob',
    target_username:  'bob',
    can_write:        true,
    expires_at:       null,
    ...over,
  }
}

function publicShare(over: Partial<PathShareInfo> = {}): PathShareInfo {
  return {
    id:         's-2',
    share_id:   's-2',
    share_type: 'public',
    can_write:  false,
    expires_at: null,
    ...over,
  }
}

describe('ShareBadge', () => {
  it('rendert ein Personen-Icon für user-Shares mit aria-Label inkl. Username', () => {
    const { getByLabelText } = renderWithI18n(
      <ShareBadge shares={[userShare()]} />,
    )
    expect(getByLabelText(/Geteilt mit bob/i)).toBeInTheDocument()
  })

  it('rendert ein Link-Icon für public-Shares', () => {
    const { getByLabelText } = renderWithI18n(
      <ShareBadge shares={[publicShare()]} />,
    )
    expect(getByLabelText(/Öffentlicher Link/i)).toBeInTheDocument()
  })

  it('rendert beide Icons nebeneinander wenn beide Typen vorhanden sind', () => {
    const { getByLabelText } = renderWithI18n(
      <ShareBadge shares={[userShare(), publicShare()]} />,
    )
    expect(getByLabelText(/Geteilt mit bob/i)).toBeInTheDocument()
    expect(getByLabelText(/Öffentlicher Link/i)).toBeInTheDocument()
  })

  it('rendert nichts wenn keine Shares vorhanden sind', () => {
    const { container } = renderWithI18n(
      <ShareBadge shares={[]} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('macht Icons klickbar wenn href gesetzt ist', () => {
    const { getByRole } = renderWithI18n(
      <ShareBadge shares={[userShare()]} href="/shares?path=/foo" />,
    )
    const link = getByRole('link', { name: /Geteilt mit bob/i })
    expect(link.getAttribute('href')).toBe('/shares?path=/foo')
  })
})
