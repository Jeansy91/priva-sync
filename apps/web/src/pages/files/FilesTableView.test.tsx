import { describe, it, expect, vi, afterEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import type { FileEntry, PathShareInfo, SharedMount } from '@privasync/api-client'
import { renderWithI18n } from '../../test/renderWithI18n'
import { FilesTableView } from './FilesTableView'
import type { DirEntry } from './helpers'
import type { FilesViewSort, MultiselectViewProps } from './filesViewTypes'

function mkFile(name: string): FileEntry {
  return {
    id: name, path: '/' + name, size: 1, mtime: new Date().toISOString(),
    content_hash: '', is_deleted: false, updated_at: new Date().toISOString(),
  }
}

function fileEntry(name: string): DirEntry {
  return { kind: 'file', name, file: mkFile(name) }
}

function mountEntry(name: string, canWrite: boolean): DirEntry {
  const mount: SharedMount = {
    id: 'm-' + name, name, path: '/' + name, is_file: true,
    can_write: canWrite, expires_at: null,
    owner: { id: 'u-a', username: 'alice', avatar_url: null },
  }
  return { kind: 'file', name, file: mkFile(name), mount }
}

function userShare(over: Partial<PathShareInfo> = {}): PathShareInfo {
  return {
    id: 's-u', share_id: 's-u', share_type: 'user', target_user_id: 'u-bob',
    target_username: 'bob', can_write: true, expires_at: null, ...over,
  }
}

function publicShare(over: Partial<PathShareInfo> = {}): PathShareInfo {
  return { id: 's-p', share_id: 's-p', share_type: 'public', can_write: false, expires_at: null, ...over }
}

const noopSort: FilesViewSort = {
  sortKey: null, sortDir: null, toggleSort: vi.fn(), setSort: vi.fn(),
}

function renderTable(over: {
  entries?: DirEntry[]
  sharesByPath?: Record<string, PathShareInfo[]>
} = {}) {
  return renderWithI18n(
    <FilesTableView
      entries={over.entries ?? [fileEntry('a.txt')]}
      selection={null}
      localeTag="de-DE"
      sort={noopSort}
      sharesByPath={over.sharesByPath ?? {}}
      onSelect={vi.fn()}
      onActivateDir={vi.fn()}
      onActivateFile={vi.fn()}
      entryCallbacks={{ onPreview: vi.fn() }}
    />,
  )
}

describe('FilesTableView — Freigabe-Spalten', () => {
  it('rendert das Personen-Icon einer user-Freigabe in einer eigenen Zelle, nicht in der Namenszelle', () => {
    const { getByText, getByLabelText } = renderTable({
      sharesByPath: { '/a.txt': [userShare()] },
    })
    const nameCell  = getByText('a.txt').closest('td')
    const badgeCell = getByLabelText(/Geteilt mit bob/i).closest('td')
    expect(badgeCell).not.toBeNull()
    expect(badgeCell).not.toBe(nameCell)
  })

  it('rendert das Link-Icon einer public-Freigabe in einer eigenen Zelle', () => {
    const { getByText, getByLabelText } = renderTable({
      sharesByPath: { '/a.txt': [publicShare()] },
    })
    const nameCell  = getByText('a.txt').closest('td')
    const linkCell  = getByLabelText(/Öffentlicher Link/i).closest('td')
    expect(linkCell).not.toBeNull()
    expect(linkCell).not.toBe(nameCell)
  })

  it('hat leere Freigabe-Spaltenköpfe mit a11y-Label (Struktur ohne Header-Icon)', () => {
    const { getByLabelText } = renderTable()
    const userHead   = getByLabelText('Personen')
    const publicHead = getByLabelText('Öffentlich')
    expect(userHead.tagName).toBe('TH')
    expect(publicHead.tagName).toBe('TH')
    // Header carries no visible icon/text — purely structural.
    expect(userHead).toHaveTextContent('')
    expect(publicHead).toHaveTextContent('')
  })

  it('ordnet die Freigabe-Spalten vor der Größe ein', () => {
    const { getByLabelText, getByText } = renderTable({
      sharesByPath: { '/a.txt': [userShare()] },
    })
    const shareCell = getByLabelText(/Geteilt mit bob/i).closest('td')!
    const sizeCell  = getByText('1 B').closest('td')!
    // DOCUMENT_POSITION_FOLLOWING (4) means sizeCell comes after shareCell.
    expect(shareCell.compareDocumentPosition(sizeCell) & Node.DOCUMENT_POSITION_FOLLOWING)
      .toBeTruthy()
  })

  it('legt bei beidseitiger Freigabe je ein Icon in getrennte Spalten', () => {
    const { getByLabelText } = renderTable({
      sharesByPath: { '/a.txt': [userShare(), publicShare()] },
    })
    const userCell   = getByLabelText(/Geteilt mit bob/i).closest('td')
    const publicCell = getByLabelText(/Öffentlicher Link/i).closest('td')
    expect(userCell).not.toBeNull()
    expect(publicCell).not.toBeNull()
    expect(userCell).not.toBe(publicCell)
  })

  it('rendert keine Freigabe-Icons für ungeteilte Einträge', () => {
    const { queryByLabelText } = renderTable({ sharesByPath: {} })
    expect(queryByLabelText(/Geteilt mit/i)).toBeNull()
    expect(queryByLabelText(/Öffentlicher Link/i)).toBeNull()
  })

  it('zeigt für einen Schreib-Mount das Schreibzugriff-Symbol in der Personen-Spalte', () => {
    const { getByText, getByLabelText } = renderTable({
      entries: [mountEntry('geteilt.txt', true)],
    })
    const nameCell   = getByText('geteilt.txt').closest('td')
    const accessCell = getByLabelText(/Lesen & Schreiben/i).closest('td')
    expect(accessCell).not.toBeNull()
    expect(accessCell).not.toBe(nameCell)
  })

  it('zeigt für einen Nur-Lese-Mount das Lesezugriff-Symbol in der Personen-Spalte', () => {
    const { getByLabelText } = renderTable({
      entries: [mountEntry('nurlesen.txt', false)],
    })
    expect(getByLabelText(/Nur Lesen/i)).not.toBeNull()
  })
})

// A `click` carrying the pointer type, as browsers dispatch it for taps.
function clickWith(el: Element, pointerType: 'touch' | 'mouse') {
  const ev = new MouseEvent('click', { bubbles: true, cancelable: true })
  Object.defineProperty(ev, 'pointerType', { value: pointerType })
  fireEvent(el, ev)
}

describe('FilesTableView — Touch-Bedienung', () => {
  function setup(multiselect?: Partial<MultiselectViewProps>) {
    const handlers = { onSelect: vi.fn(), onActivateDir: vi.fn(), onActivateFile: vi.fn() }
    const ms: MultiselectViewProps | undefined = multiselect && {
      selected: new Set(), onRowClick: vi.fn(), onCheckboxClick: vi.fn(), onToggleAll: vi.fn(),
      allSelected: false, anySelected: false, showCheckboxes: false, ...multiselect,
    }
    const utils = renderWithI18n(
      <FilesTableView
        entries={[fileEntry('a.txt'), { kind: 'dir', name: 'ordner', path: '/ordner' }]}
        selection={null}
        localeTag="de-DE"
        sort={noopSort}
        entryCallbacks={{ onPreview: vi.fn() }}
        multiselect={ms}
        {...handlers}
      />,
    )
    const row = (name: string) => utils.getByText(name).closest('tr')!
    return { ...handlers, ms, row }
  }

  it('öffnet eine Datei per Tap, statt sie nur auszuwählen', () => {
    const { row, onActivateFile, onSelect } = setup()
    clickWith(row('a.txt'), 'touch')
    expect(onActivateFile).toHaveBeenCalledTimes(1)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('betritt einen Ordner per Tap', () => {
    const { row, onActivateDir } = setup()
    clickWith(row('ordner'), 'touch')
    expect(onActivateDir).toHaveBeenCalledWith('/ordner')
  })

  it('wählt per Mausklick weiterhin nur aus', () => {
    const { row, onActivateFile, onSelect } = setup()
    clickWith(row('a.txt'), 'mouse')
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onActivateFile).not.toHaveBeenCalled()
  })

  it('schaltet bei laufender Mehrfachauswahl die Zeile per Tap um, statt zu öffnen', () => {
    const { row, ms, onActivateFile } = setup({ anySelected: true })
    clickWith(row('a.txt'), 'touch')
    expect(ms!.onCheckboxClick).toHaveBeenCalledTimes(1)
    expect(ms!.onRowClick).not.toHaveBeenCalled()
    expect(onActivateFile).not.toHaveBeenCalled()
  })
})

describe('FilesTableView — Gruppenkopf-colSpan', () => {
  afterEach(() => { vi.unstubAllGlobals() })

  // Stubs matchMedia for a viewport of `width` px, answering min-width queries.
  function stubViewport(width: number) {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
      matches: width >= Number(/min-width:\s*(\d+)px/.exec(query)?.[1] ?? 0),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })))
  }

  function renderSections() {
    return renderWithI18n(
      <FilesTableView
        sections={[{ id: 'own', header: 'Eigene', entries: [fileEntry('a.txt')] }]}
        selection={null}
        localeTag="de-DE"
        sort={noopSort}
        onSelect={vi.fn()}
        onActivateDir={vi.fn()}
        onActivateFile={vi.fn()}
        entryCallbacks={{ onPreview: vi.fn() }}
      />,
    )
  }

  it('spannt mobil nur die sichtbaren Spalten (Name + Menü), keine Phantom-Spalten', () => {
    stubViewport(375)
    const { getByText } = renderSections()
    expect(getByText('Eigene').closest('td')?.colSpan).toBe(2)
  })

  it('spannt auf Desktop-Breite alle sechs Spalten', () => {
    stubViewport(1280)
    const { getByText } = renderSections()
    expect(getByText('Eigene').closest('td')?.colSpan).toBe(6)
  })
})
