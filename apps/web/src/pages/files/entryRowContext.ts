import type { KeyboardEvent, MouseEvent } from 'react'
import type { DirEntry } from './helpers'

export type OnContextMenu = (entry: DirEntry, x: number, y: number) => void

// Touch has no double-click and no hover, so a tap must open the entry the way
// a double-click does with a mouse. `click` is a PointerEvent in current
// browsers; older ones fall back to the coarse-pointer media query.
export function isTouchClick(e: MouseEvent): boolean {
  const pointerType = (e.nativeEvent as Partial<PointerEvent>).pointerType
  if (pointerType) return pointerType !== 'mouse'
  return typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: coarse)').matches
}

// Row handlers shared by FilesTableView / FilesGridView / FilesSearchResults.
// Right-click opens the menu at the cursor; Shift+F10 or the ContextMenu key
// opens it centred on the focused row.
export function entryRowContextProps(
  entry:         DirEntry,
  onContextMenu: OnContextMenu | undefined,
) {
  if (!onContextMenu) return {}
  return {
    onContextMenu(e: MouseEvent) {
      e.preventDefault()
      // Empty-area handler on the surrounding wrapper would also fire on rows;
      // stop the event here so only the row-level menu opens.
      e.stopPropagation()
      onContextMenu(entry, e.clientX, e.clientY)
    },
    onKeyDown(e: KeyboardEvent) {
      const isMenuKey = e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')
      if (!isMenuKey) return
      e.preventDefault()
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
      onContextMenu(entry, rect.left + rect.width / 2, rect.top + rect.height / 2)
    },
  }
}
