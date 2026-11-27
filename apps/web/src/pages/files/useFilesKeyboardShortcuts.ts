import { useEffect } from 'react'
import type { SyncRoot } from '@privasync/api-client'
import type { useMultiselect } from './useMultiselect'
import type { CtxMenuState } from './useFilesPageDialogs'

interface UseFilesKeyboardShortcutsInput {
  selectedRoot: SyncRoot | null
  orderedKeys:  string[]
  multi: ReturnType<typeof useMultiselect>
  /** Live context-menu state — Esc defers to it while it's open. */
  ctxMenu: CtxMenuState | null
  /** Bulk-delete dialog opener (Delete key). */
  openBulkDelete: () => void
}

/**
 * Wires global keyboard shortcuts for the file browser:
 *   - Ctrl/Cmd+A → select every visible row
 *   - Esc       → clear the multi-selection (when the context menu is closed)
 *   - Delete    → open the bulk-delete dialog
 *
 * Skips when the focus is inside a text input or content-editable, and
 * only fires while a sync root is open.
 */
export function useFilesKeyboardShortcuts(input: UseFilesKeyboardShortcutsInput): void {
  const { selectedRoot, orderedKeys, multi, ctxMenu, openBulkDelete } = input

  useEffect(() => {
    function onKey(ev: KeyboardEvent) {
      const tgt = ev.target as HTMLElement | null
      if (tgt && (tgt.tagName === 'INPUT' || tgt.tagName === 'TEXTAREA' || tgt.isContentEditable)) return
      if (!selectedRoot) return
      if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'a') {
        ev.preventDefault()
        multi.replaceSelection(new Set(orderedKeys))
      } else if (ev.key === 'Escape' && multi.selected.size > 0) {
        // Defer to the context menu first: while it is open it owns Escape
        // (closes itself). Only clear the multi-selection on a second press.
        if (ctxMenu) return
        multi.clear()
      } else if (ev.key === 'Delete' && multi.selected.size > 0) {
        ev.preventDefault()
        openBulkDelete()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => { window.removeEventListener('keydown', onKey) }
  }, [selectedRoot, orderedKeys, multi, ctxMenu, openBulkDelete])
}
