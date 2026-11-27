import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'
import TrashPage from './TrashPage'
import { apiClient, useAuthStore } from '../store/auth'
import type { SyncRoot } from '@privasync/api-client'

const sseState = vi.hoisted(() => ({
  handlers: {} as Record<string, (d: unknown) => void>,
}))
vi.mock('../hooks/useSSE', () => ({
  useSSE: (h: Record<string, (d: unknown) => void>) => {
    sseState.handlers = h
  },
}))

beforeEach(() => {
  // Reset Zustand store between tests, otherwise trash_view/show_file_tree
  // from optimistic updates leak into subsequent tests.
  useAuthStore.setState({ user: null })
  vi.restoreAllMocks()
  vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([])
  vi.spyOn(apiClient.trash, 'list').mockResolvedValue([])
  vi.spyOn(console, 'error').mockImplementation(() => {})
  sseState.handlers = {}
})

describe('TrashPage — DE', () => {
  it('zeigt Header und Empty-State', async () => {
    const { findByRole, findByText, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    expect(await findByRole('heading', { level: 1, name: 'Papierkorb' })).toBeInTheDocument()
    expect(queryByText('Aktualisieren')).not.toBeInTheDocument()
    expect(await findByText(/Papierkorb ist leer/i)).toBeInTheDocument()
  })
})

describe('TrashPage — EN', () => {
  it('shows header and empty state without refresh button', async () => {
    const { findByRole, findByText, queryByText } = renderWithI18n(<TrashPage />, { lang: 'en' })
    expect(await findByRole('heading', { level: 1, name: 'Trash' })).toBeInTheDocument()
    expect(queryByText('Refresh')).not.toBeInTheDocument()
    expect(await findByText(/Trash is empty/i)).toBeInTheDocument()
  })
})

describe('TrashPage — SSE optimistic patch', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('add+remove entries on trash_added / trash_purged', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([])

    const { findByText, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await findByText(/Papierkorb ist leer/i)

    await act(async () => {
      sseState.handlers.trash_added?.({
        type: 'trash_added', version: 2,
        data: {
          sync_root_id: root.id, file_id: 'f-1', path: '/doc.txt',
          size: 12, trash_path: '/.trash/doc.txt',
        },
      })
    })
    expect(await findByText('doc.txt')).toBeInTheDocument()

    await act(async () => {
      sseState.handlers.trash_purged?.({ sync_root_id: root.id, file_id: 'f-1' })
    })
    await new Promise(r => setTimeout(r, 0))
    expect(queryByText('doc.txt')).not.toBeInTheDocument()
  })
})

describe('TrashPage — Row-Actions sind Icon-only mit aria-label', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('Restore-Button hat aria-label "Wiederherstellen" und keinen sichtbaren Text', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const btn = await findByRole('button', { name: 'Wiederherstellen' })
    expect(btn.closest('[title="Wiederherstellen"]')).not.toBeNull()
    // Kein sichtbares Text-Label im Button (nur Icon).
    expect(btn.textContent?.trim()).toBe('')
    // Auch nicht in versteckter Inline-Span im Button.
    expect(queryByText('Wiederherstellen', { selector: 'button > span' })).not.toBeInTheDocument()
  })

  it('Header-Button "Papierkorb leeren" hat title-Attribut', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const btn = await findByRole('button', { name: 'Papierkorb leeren' })
    expect(btn.closest('[title="Papierkorb leeren"]')).not.toBeNull()
  })

  it('Restore-Button: Titel hovert auch wenn disabled (Wrapper-Span)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    // Restore-Aufruf nie aufloesen lassen → Button bleibt im disabled-State.
    vi.spyOn(apiClient.trash, 'restore').mockReturnValue(new Promise(() => {}))
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const btn = await findByRole('button', { name: 'Wiederherstellen' })
    await u.click(btn)
    expect(btn).toBeDisabled()
    // Damit Hover funktioniert, muss ein Vorfahr mit demselben title existieren,
    // der nicht pointer-events:none ist (Button selbst hat das via disabled).
    const wrap = btn.closest('[title="Wiederherstellen"]')
    expect(wrap).not.toBeNull()
    expect(wrap).not.toBe(btn)
  })

  it('Purge-Button hat aria-label "Endgültig löschen" und Titel', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const btn = await findByRole('button', { name: 'Endgültig löschen' })
    expect(btn.closest('[title="Endgültig löschen"]')).not.toBeNull()
  })
})

describe('TrashPage — View-Toggle', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('zeigt zwei View-Toggle-Buttons mit aria-pressed', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const list = await findByRole('button', { name: 'Listenansicht' })
    const tree = await findByRole('button', { name: 'Ordneransicht' })
    expect(list).toHaveAttribute('aria-pressed', 'true')
    expect(tree).toHaveAttribute('aria-pressed', 'false')
    await u.click(tree)
    expect(list).toHaveAttribute('aria-pressed', 'false')
    expect(tree).toHaveAttribute('aria-pressed', 'true')
  })
})

describe('TrashPage — Tree-View', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const root2: SyncRoot = {
    id: 'root-2', user_id: 'u', name: 'Docs', path: '/Docs',
    created_at: new Date().toISOString(),
  }

  async function switchToTree(opts?: { expandAll?: boolean }) {
    const u = (await import('@testing-library/user-event')).default.setup()
    const result = renderWithI18n(<TrashPage />, { lang: 'de' })
    await result.findByRole('button', { name: 'Ordneransicht' })
    await u.click(await result.findByRole('button', { name: 'Ordneransicht' }))
    // Tree startet eingeklappt; Bestandstests gehen von voll aufgeklappter
    // Ansicht aus, also automatisch alle Erweitern-Buttons klicken.
    if (opts?.expandAll !== false) {
      const { within } = await import('@testing-library/react')
      // Solange noch Erweitern-Buttons sichtbar sind, weiter aufklappen.
      // Pro Klick koennen neue tiefer liegende Buttons erscheinen.
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const tree = await result.findByRole('tree')
        const buttons = within(tree).queryAllByRole('button', { name: 'Aufklappen' })
        if (buttons.length === 0) break
        for (const b of buttons) await u.click(b)
      }
    }
    return { ...result, u }
  }

  it('zeigt jeden Sync-Root mit eintraegen als Top-Knoten in einem tree', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root, root2])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    expect(within(tree).getByRole('treeitem', { name: /Photos/ })).toBeInTheDocument()
    expect(within(tree).getByRole('treeitem', { name: /Docs/ })).toBeInTheDocument()
  })

  it('zeigt empty-state in tree-view wenn keine eintraege', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root, root2])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([])
    const { findByText, queryByRole } = await switchToTree({ expandAll: false })
    expect(await findByText('Papierkorb ist leer')).toBeInTheDocument()
    expect(queryByRole('tree')).not.toBeInTheDocument()
  })

  it('blendet roots ohne entries aus, zeigt nur befuellte', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root, root2])
    vi.spyOn(apiClient.trash, 'list').mockImplementation(async (rootID: string) =>
      rootID === 'root-1'
        ? [{ id: 'f-1', path: '/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() }] as any
        : [] as any
    )
    const { findByRole, queryByRole } = await switchToTree({ expandAll: false })
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    expect(within(tree).getByRole('treeitem', { name: /Photos/ })).toBeInTheDocument()
    expect(queryByRole('treeitem', { name: /Docs/ })).not.toBeInTheDocument()
  })

  it('rendert verschachtelte Ordner aus Pfad-Segmenten', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/A/B/y.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const a = within(tree).getByRole('treeitem', { name: /^A/ })
    expect(a).toHaveAttribute('aria-level', '2')
    const b = within(tree).getByRole('treeitem', { name: /^B/ })
    expect(b).toHaveAttribute('aria-level', '3')
    expect(within(tree).getByRole('treeitem', { name: /x\.txt/ })).toHaveAttribute('aria-level', '3')
    expect(within(tree).getByRole('treeitem', { name: /y\.txt/ })).toHaveAttribute('aria-level', '4')
  })

  it('Datei-Checkbox im Tree selektiert nur diese Datei', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/A/y.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole, u } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const xRow = within(tree).getByRole('treeitem', { name: /x\.txt/ })
    const cb = within(xRow).getByRole('checkbox') as HTMLInputElement
    await u.click(cb)
    expect(cb.checked).toBe(true)
    const yRow = within(tree).getByRole('treeitem', { name: /y\.txt/ })
    expect((within(yRow).getByRole('checkbox') as HTMLInputElement).checked).toBe(false)
  })

  it('Ordner-Checkbox selektiert rekursiv alle Descendants und ist Tri-State', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/A/B/y.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole, u } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    const aCb = within(aRow).getAllByRole('checkbox')[0] as HTMLInputElement
    await u.click(aCb)
    const xCb = within(within(tree).getByRole('treeitem', { name: /x\.txt/ })).getByRole('checkbox') as HTMLInputElement
    const yCb = within(within(tree).getByRole('treeitem', { name: /y\.txt/ })).getByRole('checkbox') as HTMLInputElement
    expect(xCb.checked).toBe(true)
    expect(yCb.checked).toBe(true)
    expect(aCb.checked).toBe(true)
    expect(aCb.indeterminate).toBe(false)

    await u.click(xCb)
    expect(aCb.indeterminate).toBe(true)
    expect(aCb.checked).toBe(false)
  })

  it('Ordner-Selektion im Tree triggert Bulk-Restore für alle Descendants', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/A/B/y.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-3', path: '/C/z.txt', size: 3, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const restore = vi.spyOn(apiClient.trash, 'restore').mockResolvedValue(undefined as any)
    restore.mockClear()
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)
    const { findByRole, u } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    await u.click(within(aRow).getAllByRole('checkbox')[0])
    await u.click(await findByRole('button', { name: 'Auswahl wiederherstellen' }))
    expect(restore).toHaveBeenCalledTimes(2)
    expect(restore).toHaveBeenCalledWith('root-1', 'f-1')
    expect(restore).toHaveBeenCalledWith('root-1', 'f-2')
    expect(restore).not.toHaveBeenCalledWith('root-1', 'f-3')
  })

  it('Datei-Knoten zeigen Größe und gelöscht-am-Datum', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 2048, content_hash: '',
        deleted_at: '2026-05-10T12:34:00Z' },
    ])
    const { findByRole } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const xRow = within(tree).getByRole('treeitem', { name: /x\.txt/ })
    expect(within(xRow).getByText('2 KB')).toBeInTheDocument()
    expect(within(xRow).getByText(/10\.05\.2026/)).toBeInTheDocument()
  })

  it('Datei-Zeile im Tree hat Restore- und Purge-Action-Buttons', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const xRow = within(tree).getByRole('treeitem', { name: /x\.txt/ })
    expect(within(xRow).getByRole('button', { name: 'Wiederherstellen' })).toBeInTheDocument()
    expect(within(xRow).getByRole('button', { name: 'Endgültig löschen' })).toBeInTheDocument()
  })

  it('Ordner-Zeile hat Restore- und Purge-Action-Buttons', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    expect(within(aRow).getByRole('button', { name: 'Wiederherstellen' })).toBeInTheDocument()
    expect(within(aRow).getByRole('button', { name: 'Endgültig löschen' })).toBeInTheDocument()
  })

  it('Ordner-Restore-Button stellt alle Descendant-Dateien wieder her', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/A/B/y.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-3', path: '/C/z.txt', size: 3, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const restore = vi.spyOn(apiClient.trash, 'restore').mockResolvedValue(undefined as any)
    restore.mockClear()
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)
    const { findByRole, u } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    await u.click(within(aRow).getByRole('button', { name: 'Wiederherstellen' }))
    expect(restore).toHaveBeenCalledTimes(2)
    expect(restore).toHaveBeenCalledWith('root-1', 'f-1')
    expect(restore).toHaveBeenCalledWith('root-1', 'f-2')
    expect(restore).not.toHaveBeenCalledWith('root-1', 'f-3')
  })

  it('Ordner-Purge-Button öffnet Confirm und löscht Descendants', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/A/B/y.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-3', path: '/C/z.txt', size: 3, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const purge = vi.spyOn(apiClient.trash, 'purge').mockResolvedValue(undefined as any)
    purge.mockClear()
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)
    const { findByRole, u } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    await u.click(within(aRow).getByRole('button', { name: 'Endgültig löschen' }))
    const dialog = await findByRole('dialog')
    expect(within(dialog).getByText(/2 ausgewählte Dateien/)).toBeInTheDocument()
    await u.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))
    expect(purge).toHaveBeenCalledTimes(2)
    expect(purge).toHaveBeenCalledWith('root-1', 'f-1')
    expect(purge).toHaveBeenCalledWith('root-1', 'f-2')
    expect(purge).not.toHaveBeenCalledWith('root-1', 'f-3')
  })

  it('Tree benutzt dieselben Tabellen-Spalten (Datei | Größe | Gelöscht am)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = await switchToTree()
    const { within } = await import('@testing-library/react')
    const tree = await findByRole('tree')
    const table = tree.closest('table')!
    const head = within(table).getAllByRole('columnheader').map((th) => th.textContent?.trim())
    expect(head).toContain('Datei')
    expect(head).toContain('Größe')
    expect(head).toContain('Gelöscht am')
  })

  it('Ordner-Knoten zeigen aggregierte Größe und neueste Löschzeit', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1024, content_hash: '',
        deleted_at: '2026-05-09T10:00:00Z' },
      { id: 'f-2', path: '/A/B/y.txt', size: 3072, content_hash: '',
        deleted_at: '2026-05-12T08:00:00Z' },
    ])
    const { findByRole } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    expect(within(aRow).getByText('4 KB')).toBeInTheDocument()
    expect(within(aRow).getByText(/12\.05\.2026/)).toBeInTheDocument()
  })

  it('startet im eingeklappten zustand — nur sync-roots sichtbar', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole, queryByRole } = await switchToTree({ expandAll: false })
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    // Root sichtbar, A und x.txt versteckt.
    expect(within(tree).getByRole('treeitem', { name: /Photos/ })).toBeInTheDocument()
    expect(queryByRole('treeitem', { name: /^A/ })).not.toBeInTheDocument()
    expect(queryByRole('treeitem', { name: /x\.txt/ })).not.toBeInTheDocument()
  })

  it('initialer viewMode kommt aus user.trash_view', async () => {
    useAuthStore.setState({
      user: { id: 'u', username: 'u', email: 'a', role: 'user',
        totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
        avatar_url: null, preferred_language: 'de', show_file_tree: false,
        trash_view: 'tree' } as any,
    })
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    expect(await findByRole('tree')).toBeInTheDocument()
  })

  it('Wechsel der Ansicht persistiert via apiClient + auth-store', async () => {
    useAuthStore.setState({
      user: { id: 'u', username: 'u', email: 'a', role: 'user',
        totp_enabled: false, quota_bytes: 0, used_bytes: 0, trash_bytes: 0,
        avatar_url: null, preferred_language: 'de', show_file_tree: false,
        trash_view: 'list' } as any,
    })
    const setView = vi.spyOn(apiClient.auth, 'setTrashView').mockResolvedValue(undefined as any)
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await u.click(await findByRole('button', { name: 'Ordneransicht' }))
    await vi.waitFor(() => expect(setView).toHaveBeenCalledWith('tree'))
    expect(useAuthStore.getState().user?.trash_view).toBe('tree')
  })

  it('Ordner-Toggle kollabiert und versteckt Kinder', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/A/x.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole, queryByRole, u } = await switchToTree()
    const tree = await findByRole('tree')
    const { within } = await import('@testing-library/react')
    expect(within(tree).getByRole('treeitem', { name: /x\.txt/ })).toBeInTheDocument()
    const aRow = within(tree).getByRole('treeitem', { name: /^A/ })
    const collapse = within(aRow).getByRole('button', { name: 'Zuklappen' })
    await u.click(collapse)
    expect(queryByRole('treeitem', { name: /x\.txt/ })).not.toBeInTheDocument()
  })
})

describe('TrashPage — Multiselect (Liste)', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }

  it('rendert eine Checkbox pro Zeile mit aria-label = Dateiname', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const cb1 = await findByRole('checkbox', { name: 'a.txt' })
    const cb2 = await findByRole('checkbox', { name: 'b.txt' })
    expect(cb1).toBeInTheDocument()
    expect(cb2).toBeInTheDocument()
    expect((cb1 as HTMLInputElement).checked).toBe(false)
  })

  it('Klick auf Zeilen-Checkbox toggelt aria-checked', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const cb = await findByRole('checkbox', { name: 'a.txt' }) as HTMLInputElement
    expect(cb.checked).toBe(false)
    await u.click(cb)
    expect(cb.checked).toBe(true)
    await u.click(cb)
    expect(cb.checked).toBe(false)
  })

  it('Header-Checkbox toggelt alle Zeilen und spiegelt Tri-State', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const head = await findByRole('checkbox', { name: 'Alle auswählen' }) as HTMLInputElement
    const cb1 = await findByRole('checkbox', { name: 'a.txt' }) as HTMLInputElement
    const cb2 = await findByRole('checkbox', { name: 'b.txt' }) as HTMLInputElement

    expect(head.checked).toBe(false)
    expect(head.indeterminate).toBe(false)

    await u.click(head)
    expect(cb1.checked).toBe(true)
    expect(cb2.checked).toBe(true)
    expect(head.checked).toBe(true)

    await u.click(head)
    expect(cb1.checked).toBe(false)
    expect(cb2.checked).toBe(false)

    await u.click(cb1)
    expect(head.indeterminate).toBe(true)
    expect(head.checked).toBe(false)
  })

  it('Shift-Klick selektiert Range zwischen Anchor und Ziel', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-3', path: '/c.txt', size: 3, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    const a = await findByRole('checkbox', { name: 'a.txt' }) as HTMLInputElement
    const b = await findByRole('checkbox', { name: 'b.txt' }) as HTMLInputElement
    const c = await findByRole('checkbox', { name: 'c.txt' }) as HTMLInputElement
    await u.click(a)
    await u.keyboard('{Shift>}')
    await u.click(c)
    await u.keyboard('{/Shift}')
    // The range is applied by a state update after the click; on a loaded CI
    // runner it has not always landed when the click promise resolves.
    await waitFor(() => expect(b.checked).toBe(true))
    expect(a.checked).toBe(true)
    expect(c.checked).toBe(true)
  })

  it('Header behält Titel und "Papierkorb leeren" auch bei aktiver Selektion', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, queryByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await u.click(await findByRole('checkbox', { name: 'a.txt' }))
    expect(queryByRole('heading', { level: 1, name: 'Papierkorb' })).toBeInTheDocument()
    expect(queryByRole('button', { name: 'Papierkorb leeren' })).toBeInTheDocument()
  })

  it('zeigt Action-Bar mit Anzahl und drei Buttons bei N≥1', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const { within } = await import('@testing-library/react')
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await findByRole('checkbox', { name: 'a.txt' })
    expect(queryByText(/ausgewählt/)).not.toBeInTheDocument()
    await u.click(await findByRole('checkbox', { name: 'a.txt' }))
    const bar = await findByRole('toolbar', { name: 'Auswahl' })
    expect(bar.textContent).toMatch(/Ausgewählt:\s*1 B/)
    await u.click(await findByRole('checkbox', { name: 'b.txt' }))
    expect(bar.textContent).toMatch(/Ausgewählt:\s*3 B/)
    expect(within(bar).getByRole('button', { name: 'Auswahl wiederherstellen' })).toBeInTheDocument()
    expect(within(bar).getByRole('button', { name: 'Auswahl endgültig löschen' })).toBeInTheDocument()
    expect(within(bar).getByRole('button', { name: 'Auswahl aufheben' })).toBeInTheDocument()
  })

  it('Bulk-Restore ruft apiClient.trash.restore für jede Selektion und entfernt Zeilen', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-3', path: '/c.txt', size: 3, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const restore = vi.spyOn(apiClient.trash, 'restore').mockResolvedValue(undefined as any)
    restore.mockClear()
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await u.click(await findByRole('checkbox', { name: 'a.txt' }))
    await u.click(await findByRole('checkbox', { name: 'c.txt' }))
    await u.click(await findByRole('button', { name: 'Auswahl wiederherstellen' }))
    expect(restore).toHaveBeenCalledWith('root-1', 'f-1')
    expect(restore).toHaveBeenCalledWith('root-1', 'f-3')
    expect(restore).toHaveBeenCalledTimes(2)
    expect(queryByText('a.txt')).not.toBeInTheDocument()
    expect(queryByText('c.txt')).not.toBeInTheDocument()
    expect(queryByText('b.txt')).toBeInTheDocument()
  })

  it('Bulk-Purge öffnet Confirm und ruft purge für jede Selektion', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
      { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const purge = vi.spyOn(apiClient.trash, 'purge').mockResolvedValue(undefined as any)
    purge.mockClear()
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await u.click(await findByRole('checkbox', { name: 'a.txt' }))
    await u.click(await findByRole('checkbox', { name: 'b.txt' }))
    await u.click(await findByRole('button', { name: 'Auswahl endgültig löschen' }))
    // Confirm-Dialog erscheint mit selected-Description
    const { within } = await import('@testing-library/react')
    const dialog = await findByRole('dialog')
    expect(within(dialog).getByText('Endgültig löschen?')).toBeInTheDocument()
    expect(within(dialog).getByText(/2 ausgewählte Dateien/)).toBeInTheDocument()
    await u.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))
    expect(purge).toHaveBeenCalledTimes(2)
    expect(purge).toHaveBeenCalledWith('root-1', 'f-1')
    expect(purge).toHaveBeenCalledWith('root-1', 'f-2')
  })

  it('Auswahl aufheben leert Selektion', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue([
      { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    ])
    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, queryByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await u.click(await findByRole('checkbox', { name: 'a.txt' }))
    await u.click(await findByRole('button', { name: 'Auswahl aufheben' }))
    expect(queryByRole('toolbar', { name: 'Auswahl' })).not.toBeInTheDocument()
  })
})

describe('TrashPage — Papierkorb leeren (Bulk)', () => {
  const root: SyncRoot = {
    id: 'root-1', user_id: 'u', name: 'Photos', path: '/Photos',
    created_at: new Date().toISOString(),
  }
  const twoEntries = [
    { id: 'f-1', path: '/a.txt', size: 1, content_hash: '', deleted_at: new Date().toISOString() },
    { id: 'f-2', path: '/b.txt', size: 2, content_hash: '', deleted_at: new Date().toISOString() },
  ]

  it('ruft Bulk-Endpoint einmal und kein per-entry purge', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue(twoEntries)
    const empty = vi.spyOn(apiClient.trash, 'empty').mockResolvedValue(undefined as any)
    const purge = vi.spyOn(apiClient.trash, 'purge').mockResolvedValue(undefined as any)
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)

    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await u.click(await findByRole('button', { name: 'Papierkorb leeren' }))
    const { within } = await import('@testing-library/react')
    const dialog = await findByRole('dialog')
    await u.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))

    expect(empty).toHaveBeenCalledTimes(1)
    expect(purge).not.toHaveBeenCalled()
  })

  it('leert Liste bei Erfolg ohne Fehlermeldung', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue(twoEntries)
    vi.spyOn(apiClient.trash, 'empty').mockResolvedValue(undefined as any)
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)

    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, findByText, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await findByText('a.txt')
    await u.click(await findByRole('button', { name: 'Papierkorb leeren' }))
    const { within } = await import('@testing-library/react')
    const dialog = await findByRole('dialog')
    await u.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))

    await findByText(/Papierkorb ist leer/i)
    expect(queryByText('a.txt')).not.toBeInTheDocument()
    expect(queryByText('b.txt')).not.toBeInTheDocument()
  })

  it('behält Liste und zeigt Fehler bei Bulk-Fehler (all-or-nothing)', async () => {
    vi.spyOn(apiClient.files, 'listSyncRoots').mockResolvedValue([root])
    vi.spyOn(apiClient.trash, 'list').mockResolvedValue(twoEntries)
    vi.spyOn(apiClient.trash, 'empty').mockRejectedValue(new Error('boom'))
    vi.spyOn(apiClient.auth, 'me').mockResolvedValue({} as any)

    const u = (await import('@testing-library/user-event')).default.setup()
    const { findByRole, findByText, queryByText } = renderWithI18n(<TrashPage />, { lang: 'de' })
    await findByText('a.txt')
    await u.click(await findByRole('button', { name: 'Papierkorb leeren' }))
    const { within } = await import('@testing-library/react')
    const dialog = await findByRole('dialog')
    await u.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))

    expect(await findByText('a.txt')).toBeInTheDocument()
    expect(await findByText('b.txt')).toBeInTheDocument()
    expect(queryByText(/Papierkorb ist leer/i)).not.toBeInTheDocument()
  })
})
