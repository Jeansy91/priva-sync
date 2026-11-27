import { describe, it, expect, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import { FilesPageTree } from './FilesPageTree'
import type { SyncRoot, SharedMount } from '@privasync/api-client'

const syncRoots: SyncRoot[] = [
  { id: 'r1', user_id: 'u', name: 'Photos', path: '/Photos', created_at: '' },
]

function mount(id: string, name: string): SharedMount {
  return {
    id, name, path: '/' + name, is_file: false, can_write: false, expires_at: null,
    owner: { id: 'o', username: 'alice', avatar_url: null },
  }
}

function render(go: ReturnType<typeof vi.fn>, mounts: SharedMount[] = []) {
  return renderWithI18n(
    <FilesPageTree
      syncRoots={syncRoots}
      mounts={mounts}
      activeRootID={null}
      activePath="/"
      reloadToken={0}
      go={go as never}
      width={240}
      min={160}
      max={480}
      startResize={() => {}}
      onKeyDown={() => {}}
    />
  )
}

describe('<FilesPageTree>', () => {
  it('navigiert beim klick auf einen sync-root zur files-url', async () => {
    const go = vi.fn()
    const u = userEvent.setup()
    const { getByText } = render(go)
    await u.click(getByText('Photos'))
    expect(go).toHaveBeenCalledWith('/files/Photos')
  })

  it('navigiert beim klick auf einen mount zur mount-url', async () => {
    const go = vi.fn()
    const u = userEvent.setup()
    const { getByText } = render(go, [mount('m1', 'Team')])
    await u.click(getByText('Team'))
    expect(go).toHaveBeenCalledWith('/files/_m/m1')
  })
})
