import { describe, it, expect, beforeEach, vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/renderWithI18n'
import { FileTree } from './FileTree'
import { apiClient } from '../../store/auth'
import type { SyncRoot, FileEntry, SharedMount } from '@privasync/api-client'

const roots: SyncRoot[] = [
  { id: 'r1', user_id: 'u', name: 'Photos', path: '/Photos', created_at: '' },
  { id: 'r2', user_id: 'u', name: 'Docs',   path: '/Docs',   created_at: '' },
]

function mount(id: string, name: string, is_file = false): SharedMount {
  return {
    id, name, path: '/' + name, is_file, can_write: false, expires_at: null,
    owner: { id: 'o', username: 'alice', avatar_url: null },
  }
}

function file(id: string, path: string): FileEntry {
  return { id, path, size: 1, mtime: '', content_hash: '', is_deleted: false, updated_at: '' }
}

function dir(id: string, path: string): FileEntry {
  return { id, path, size: 0, mtime: '', content_hash: '', is_deleted: false, updated_at: '', is_dir: true }
}

function listResp(files: FileEntry[]) {
  return { files, next_cursor: '' }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('<FileTree>', () => {
  it('setzt title-attribut auf knoten-labels fuer truncation-tooltip', () => {
    const { getByText } = renderWithI18n(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={() => {}} />
    )
    expect(getByText('Photos')).toHaveAttribute('title', 'Photos')
    expect(getByText('Docs')).toHaveAttribute('title', 'Docs')
  })

  it('rendert alle SyncRoots als Top-Level-Knoten', () => {
    const { getByText } = renderWithI18n(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={() => {}} />
    )
    expect(getByText('Photos')).toBeInTheDocument()
    expect(getByText('Docs')).toBeInTheDocument()
  })

  it('expandiert root, laedt files und zeigt direkte unterordner', async () => {
    const list = vi.spyOn(apiClient.files, 'list').mockResolvedValue(listResp([
      dir('d1', '/Vacation'),
      dir('d2', '/Family'),
    ]))
    const u = userEvent.setup()

    const { getByLabelText, findByText } = renderWithI18n(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={() => {}} />
    )
    await u.click(getByLabelText('Photos erweitern'))

    expect(list).toHaveBeenCalledWith('r1', '/')
    expect(await findByText('Family')).toBeInTheDocument()
    expect(await findByText('Vacation')).toBeInTheDocument()
  })

  it('rendert Dir-Mounts als Top-Level-Knoten unter "Mit mir geteilt"', () => {
    const { getByText } = renderWithI18n(
      <FileTree
        roots={roots}
        mounts={[mount('m1', 'Team')]}
        activeRootID={null}
        activePath="/"
        onNavigate={() => {}}
      />
    )
    expect(getByText('Mit mir geteilt')).toBeInTheDocument()
    expect(getByText('Team')).toBeInTheDocument()
  })

  it('blendet File-Mounts im Baum aus (nur Ordner browsbar)', () => {
    const { queryByText, getByText } = renderWithI18n(
      <FileTree
        roots={roots}
        mounts={[mount('m1', 'Team'), mount('m2', 'report.pdf', true)]}
        activeRootID={null}
        activePath="/"
        onNavigate={() => {}}
      />
    )
    expect(getByText('Team')).toBeInTheDocument()
    expect(queryByText('report.pdf')).toBeNull()
  })

  it('expandiert mount via sharedMounts.files und zeigt unterordner', async () => {
    const mountFiles = vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [
        { name: 'Reports', path: '/Reports', is_dir: true,  size: 0 },
        { name: 'memo.txt', path: '/memo.txt', is_dir: false, size: 1 },
      ],
    })
    const list = vi.spyOn(apiClient.files, 'list')
    const u = userEvent.setup()

    const { getByLabelText, findByText } = renderWithI18n(
      <FileTree
        roots={roots}
        mounts={[mount('m1', 'Team')]}
        activeRootID={null}
        activePath="/"
        onNavigate={() => {}}
      />
    )
    await u.click(getByLabelText('Team erweitern'))

    expect(mountFiles).toHaveBeenCalledWith('m1', '/')
    expect(list).not.toHaveBeenCalledWith('m1', '/')
    expect(await findByText('Reports')).toBeInTheDocument()
  })

  it('ruft onNavigate beim klick auf einen knoten', async () => {
    const onNavigate = vi.fn()
    const u = userEvent.setup()
    const { getByText } = renderWithI18n(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={onNavigate} />
    )
    await u.click(getByText('Docs'))
    expect(onNavigate).toHaveBeenCalledWith('r2', '/')
  })

  it('zeigt mount-unterordner statt einer dublette des mounts selbst', async () => {
    // Server returns GIVER-ABSOLUTE paths (e.path = "/Team/Reports"), so the
    // subdir extraction must key off e.name, not e.path — otherwise the
    // mount's own root folder ("Team") shows up as its own child.
    vi.spyOn(apiClient.sharedMounts, 'files').mockResolvedValue({
      entries: [
        { name: 'Reports', path: '/Team/Reports', is_dir: true, size: 0 },
      ],
    })
    const u = userEvent.setup()

    const { getByLabelText, findByText, queryAllByText } = renderWithI18n(
      <FileTree
        roots={[]}
        mounts={[mount('m1', 'Team')]}
        activeRootID={null}
        activePath="/"
        onNavigate={() => {}}
      />
    )
    await u.click(getByLabelText('Team erweitern'))

    expect(await findByText('Reports')).toBeInTheDocument()
    // "Team" appears exactly once — the top-level node, no duplicate child.
    expect(queryAllByText('Team')).toHaveLength(1)
  })

  it('ruft onNavigate mit der mount-id beim klick auf einen mount-knoten', async () => {
    const onNavigate = vi.fn()
    const u = userEvent.setup()
    const { getByText } = renderWithI18n(
      <FileTree
        roots={roots}
        mounts={[mount('m1', 'Team')]}
        activeRootID={null}
        activePath="/"
        onNavigate={onNavigate}
      />
    )
    await u.click(getByText('Team'))
    expect(onNavigate).toHaveBeenCalledWith('m1', '/')
  })

  it('expandiert beim mount entlang activePath und markiert aktiven knoten', async () => {
    const list = vi.spyOn(apiClient.files, 'list').mockImplementation(async (_rootID, path) => {
      if (path === '/')    return listResp([dir('d1', '/Sub')])
      if (path === '/Sub') return listResp([dir('d2', '/Sub/inner')])
      return listResp([])
    })

    const { findByText } = renderWithI18n(
      <FileTree roots={roots} activeRootID="r1" activePath="/Sub/inner" onNavigate={() => {}} />
    )
    // 'inner' wird nur sichtbar, wenn '/' und '/Sub' beide aufgeklappt + geladen sind.
    const inner = await findByText('inner')
    expect(inner).toBeInTheDocument()
    expect(list).toHaveBeenCalledWith('r1', '/')
    expect(list).toHaveBeenCalledWith('r1', '/Sub')

    // Aktiver Knoten traegt aria-current=location
    const row = inner.closest('[aria-current="location"]')
    expect(row).not.toBeNull()
  })

  it('expandiert aktiven mount-pfad via sharedMounts und markiert ihn', async () => {
    const mountFiles = vi.spyOn(apiClient.sharedMounts, 'files').mockImplementation(async (_id, path) => {
      if (path === '/') return { entries: [{ name: 'Sub', path: '/Sub', is_dir: true, size: 0 }] }
      return { entries: [] }
    })

    const { findByText } = renderWithI18n(
      <FileTree
        roots={roots}
        mounts={[mount('m1', 'Team')]}
        activeRootID="m1"
        activePath="/Sub"
        onNavigate={() => {}}
      />
    )
    const sub = await findByText('Sub')
    expect(sub).toBeInTheDocument()
    expect(mountFiles).toHaveBeenCalledWith('m1', '/')
    expect(sub.closest('[aria-current="location"]')).not.toBeNull()
  })

  it('reload-token-bump laedt expandierte pfade neu', async () => {
    const list = vi.spyOn(apiClient.files, 'list').mockResolvedValue(listResp([
      dir('d1', '/Sub'),
    ]))
    const u = userEvent.setup()

    const { getByLabelText, findByText, rerender } = renderWithI18n(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={() => {}} reloadToken={0} />
    )
    await u.click(getByLabelText('Photos erweitern'))
    expect(await findByText('Sub')).toBeInTheDocument()
    const callsBefore = list.mock.calls.length

    rerender(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={() => {}} reloadToken={1} />
    )
    // at least one extra list call for the expanded root path '/'
    await vi.waitFor(() => {
      expect(list.mock.calls.length).toBeGreaterThan(callsBefore)
    })
    expect(list).toHaveBeenLastCalledWith('r1', '/')
  })

  it('haelt expand-state ueber re-render hinweg', async () => {
    vi.spyOn(apiClient.files, 'list').mockResolvedValue(listResp([
      dir('d1', '/Sub'),
    ]))
    const u = userEvent.setup()

    const { getByLabelText, findByText, rerender } = renderWithI18n(
      <FileTree roots={roots} activeRootID={null} activePath="/" onNavigate={() => {}} />
    )
    await u.click(getByLabelText('Photos erweitern'))
    expect(await findByText('Sub')).toBeInTheDocument()

    rerender(<FileTree roots={roots} activeRootID="r1" activePath="/Sub" onNavigate={() => {}} />)
    // 'Sub' bleibt sichtbar
    expect(await findByText('Sub')).toBeInTheDocument()
  })
})
