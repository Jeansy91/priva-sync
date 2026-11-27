import { useRef, useCallback } from 'react'
import { useLasso } from './useLasso'
import type { RowRect } from './lassoIntersect'
import type { useMultiselect } from './useMultiselect'

interface UseFilesLassoInput {
  /** Live multiselect handle from useMultiselect. */
  multi: ReturnType<typeof useMultiselect>
  /** Cleared on empty-area click without modifier. */
  setDetailSelection:    (value: null) => void
  setSelectedSyncRootID: (value: null) => void
}

/**
 * Encapsulates the lasso-selection wiring used by the file table/grid:
 * row hit-testing, additive (Ctrl) baseline tracking, and the
 * empty-area mousedown handler that clears single-row focus before
 * starting the drag.
 */
export function useFilesLasso(input: UseFilesLassoInput) {
  const { multi, setDetailSelection, setSelectedSyncRootID } = input

  const lassoBaselineRef = useRef<Set<string>>(new Set())

  const lasso = useLasso({
    isRowTarget: (tgt) => {
      if (!(tgt instanceof Element)) return false
      return tgt.closest('[data-entry-key]') !== null
    },
    collectRows: (container) => {
      const rows: RowRect[] = []
      const box = container.getBoundingClientRect()
      const els = container.querySelectorAll<HTMLElement>('[data-entry-key]')
      els.forEach((el) => {
        const key = el.getAttribute('data-entry-key')
        if (!key) return
        const r = el.getBoundingClientRect()
        rows.push({
          path: key,
          rect: {
            x: r.left - box.left + container.scrollLeft,
            y: r.top  - box.top  + container.scrollTop,
            w: r.width, h: r.height,
          },
        })
      })
      return rows
    },
    onChange: (hits, additive) => {
      const base = additive ? lassoBaselineRef.current : new Set<string>()
      const next = new Set(base)
      for (const h of hits) next.add(h)
      multi.replaceSelection(next)
    },
  })

  const handleLassoMouseDown = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    if (!(e.target instanceof Element)) return
    if (e.target.closest('[data-entry-key]')) return
    // Interactive controls living outside the rows (e.g. the header select-all
    // checkbox, toolbar buttons) must not count as an empty-area mousedown —
    // that would clear the selection and restart the lasso underneath them.
    if (e.target.closest('input, button, a, select, [role="checkbox"], [role="button"]')) return
    // Empty-area mousedown without modifier clears both the multi-selection
    // and the single-row detail focus. A drag will then refill multi via the
    // lasso. With modifier, ctrl-lasso adds to the existing set.
    const additive = e.ctrlKey || e.metaKey
    if (!additive) {
      multi.clear()
      setDetailSelection(null)
      setSelectedSyncRootID(null)
    }
    lassoBaselineRef.current = additive ? new Set(multi.selected) : new Set()
    lasso.onMouseDown(e)
  }, [lasso, multi, setDetailSelection, setSelectedSyncRootID])

  return { lasso, handleLassoMouseDown }
}
