import { describe, it, expect, vi } from 'vitest'
import type { FileEntry, PathShareInfo } from '@privasync/api-client'
import { renderWithI18n } from '../../test/renderWithI18n'
import { FilesGridView } from './FilesGridView'
import type { DirEntry } from './helpers'

function mkFile(name: string): FileEntry {
  return {
    id: name, path: '/' + name, size: 1, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
  }
}

function fileEntry(name: string): DirEntry {
  return { kind: 'file', name, file: mkFile(name) }
}

function userShare(): PathShareInfo {
  return {
    id: 's-u', share_id: 's-u', share_type: 'user', target_user_id: 'u-bob',
    target_username: 'bob', can_write: true, expires_at: null,
  }
}

function renderGrid(sharesByPath: Record<string, PathShareInfo[]>) {
  return renderWithI18n(
    <FilesGridView
      entries={[fileEntry('a.txt')]}
      selection={null}
      sharesByPath={sharesByPath}
      onSelect={vi.fn()}
      onActivateDir={vi.fn()}
      onActivateFile={vi.fn()}
    />,
  )
}

describe('FilesGridView — Freigabe-Icon', () => {
  it('rendert das Freigabe-Icon außerhalb des Namens-Spans', () => {
    const { getByLabelText, container } = renderGrid({ '/a.txt': [userShare()] })
    const badge   = getByLabelText(/Geteilt mit bob/i)
    const nameBox = container.querySelector('[title="a.txt"]')
    expect(badge).toBeInTheDocument()
    expect(nameBox).not.toBeNull()
    expect(nameBox?.contains(badge)).toBe(false)
  })
})
