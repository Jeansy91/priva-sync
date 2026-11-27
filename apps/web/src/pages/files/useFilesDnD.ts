import { useCallback, useRef, useState } from 'react'
import type { DirEntry } from './helpers'
import { isAncestorOrEqual, parentPath } from './helpers'
import {
  entryKey, keyToPath, type EntryKey, type FilesDnd,
} from './filesViewTypes'
import type { UseMultiselect } from './useMultiselect'

// Custom drag payload marker. Internal moves set this on the dataTransfer so
// the page-level upload dropzone can tell them apart from OS file drags and
// skip its overlay. The value is irrelevant — presence of the type is the
// signal.
export const DND_MIME = 'application/x-privasync-move'

// True when the drag carries our internal-move marker (vs. an OS file drag).
// `types` is the only field readable during dragover, so the marker has to be
// a MIME type, not payload data.
export function isInternalDrag(e: React.DragEvent): boolean {
  return e.dataTransfer.types.includes(DND_MIME)
}

interface UseFilesDnDInput {
  multi:  UseMultiselect
  moveTo: (targetDir: string, keys: Iterable<EntryKey>) => void | Promise<void>
  // Drag-and-drop is disabled at the root list and inside read-only mounts.
  enabled: boolean
}

/**
 * Headless drag-and-drop move wiring for the file listing. Owns the dragged
 * key set, the hovered drop target, and the validation that keeps a folder
 * from being dropped into itself or its own subtree. The single instance is
 * threaded into the rows/tiles (drag sources + folder drop targets), the
 * breadcrumbs, and the tree (drop targets).
 */
export function useFilesDnD({ multi, moveTo, enabled }: UseFilesDnDInput): FilesDnd {
  const [overTarget, setOverTarget] = useState<string | null>(null)
  // Ref, not state: read synchronously inside dragover/drop without forcing a
  // re-render per dragover tick.
  const draggedRef = useRef<Set<EntryKey>>(new Set())

  const canDrop = useCallback((targetDir: string): boolean => {
    const keys = draggedRef.current
    if (keys.size === 0) return false
    for (const k of keys) {
      const { kind, path } = keyToPath(k)
      // A directory cannot move into itself or any of its descendants.
      if (kind === 'dir' && isAncestorOrEqual(path, targetDir)) return false
      // No-op: the entry already lives directly in the target directory.
      if (parentPath(path) === targetDir) return false
    }
    return true
  }, [])

  const dragProps = useCallback((entry: DirEntry) => ({
    draggable: enabled,
    onDragStart: (e: React.DragEvent) => {
      const k = entryKey(entry)
      // Grabbing a row that is part of an active multi-selection drags the
      // whole set; otherwise just the grabbed row travels.
      const keys = multi.selected.has(k) && multi.selected.size > 1
        ? new Set(multi.selected)
        : new Set([k])
      draggedRef.current = keys
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData(DND_MIME, String(keys.size))
    },
    onDragEnd: () => {
      draggedRef.current = new Set()
      setOverTarget(null)
    },
  }), [enabled, multi.selected])

  const dropProps = useCallback((targetDir: string) => ({
    onDragOver: (e: React.DragEvent) => {
      if (!canDrop(targetDir)) return
      // preventDefault marks this element as a valid drop target; without it
      // the browser shows the no-drop cursor and never fires onDrop.
      e.preventDefault()
      e.dataTransfer.dropEffect = 'move'
      setOverTarget(targetDir)
    },
    onDragLeave: () => {
      setOverTarget((prev) => (prev === targetDir ? null : prev))
    },
    onDrop: (e: React.DragEvent) => {
      e.preventDefault()
      // Stop the drop from bubbling to the page-level upload handler.
      e.stopPropagation()
      const keys = draggedRef.current
      setOverTarget(null)
      const ok = canDrop(targetDir)
      draggedRef.current = new Set()
      if (ok) void moveTo(targetDir, keys)
    },
  }), [canDrop, moveTo])

  const isDropActive = useCallback(
    (targetDir: string) => overTarget === targetDir,
    [overTarget],
  )

  return { dragProps, dropProps, isDropActive }
}
