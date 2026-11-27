import { describe, it, expect, vi } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { EntryContextMenu } from './EntryContextMenu'
import type { DirEntry } from './helpers'

const dir: DirEntry = { kind: 'dir', name: 'docs', path: '/docs' }

const cb = () => ({
  onDelete:   vi.fn(),
  onDownload: vi.fn(),
  onPreview:  vi.fn(),
  onShare:    vi.fn(),
})

describe('EntryContextMenu', () => {
  it('rendert nichts wenn anchor null', () => {
    const { container } = renderWithI18n(
      <EntryContextMenu anchor={null} entries={[dir]} callbacks={cb()} onClose={() => {}} />,
    )
    expect(container.textContent).toBe('')
  })

  it('zeigt Items wenn anchor gesetzt', () => {
    const { getByRole } = renderWithI18n(
      <EntryContextMenu
        anchor={{ x: 10, y: 10 }} entries={[dir]} callbacks={cb()} onClose={() => {}}
      />,
    )
    expect(getByRole('menuitem', { name: /Als ZIP herunterladen/ })).toBeTruthy()
    expect(getByRole('menuitem', { name: /Teilen/ })).toBeTruthy()
    expect(getByRole('menuitem', { name: /Löschen/ })).toBeTruthy()
  })

  it('Esc ruft onClose', () => {
    const onClose = vi.fn()
    renderWithI18n(
      <EntryContextMenu
        anchor={{ x: 10, y: 10 }} entries={[dir]} callbacks={cb()} onClose={onClose}
      />,
    )
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('Click ausserhalb ruft onClose', () => {
    const onClose = vi.fn()
    renderWithI18n(
      <EntryContextMenu
        anchor={{ x: 10, y: 10 }} entries={[dir]} callbacks={cb()} onClose={onClose}
      />,
    )
    fireEvent.mouseDown(document.body)
    expect(onClose).toHaveBeenCalled()
  })

  it('Klick auf Item ruft Callback und schliesst', () => {
    const onClose = vi.fn()
    const c = cb()
    const { getByRole } = renderWithI18n(
      <EntryContextMenu
        anchor={{ x: 10, y: 10 }} entries={[dir]} callbacks={c} onClose={onClose}
      />,
    )
    fireEvent.click(getByRole('menuitem', { name: /Löschen/ }))
    expect(c.onDelete).toHaveBeenCalledWith(dir)
    expect(onClose).toHaveBeenCalled()
  })
})
