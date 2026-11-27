import { useCallback, useRef, useState } from 'react'

export interface UseMultiselect {
  selected: Set<string>
  anchor:   string | null
  handleClick:      (path: string, e: React.MouseEvent, orderedPaths: string[]) => void
  clear:            () => void
  toggleAll:        (orderedPaths: string[]) => void
  replaceSelection: (next: Set<string>) => void
  addSelection:     (paths: string[]) => void
}

export function useMultiselect(): UseMultiselect {
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [anchor,   setAnchorState] = useState<string | null>(null)
  const anchorRef = useRef<string | null>(null)
  const setAnchor = (v: string | null) => { anchorRef.current = v; setAnchorState(v) }

  const handleClick = useCallback((path: string, e: React.MouseEvent, orderedPaths: string[]) => {
    if (e.shiftKey) {
      setSelected((prev) => {
        if (!anchorRef.current) return new Set([path])
        const ai = orderedPaths.indexOf(anchorRef.current)
        const ti = orderedPaths.indexOf(path)
        if (ai < 0 || ti < 0) return new Set([...prev, path])
        const [lo, hi] = ai <= ti ? [ai, ti] : [ti, ai]
        const next = new Set(prev)
        for (let i = lo; i <= hi; i++) next.add(orderedPaths[i])
        return next
      })
      return
    }
    if (e.ctrlKey || e.metaKey) {
      setSelected((prev) => {
        const next = new Set(prev)
        if (next.has(path)) next.delete(path)
        else next.add(path)
        return next
      })
      setAnchor(path)
      return
    }
    setSelected(new Set([path]))
    setAnchor(path)
  }, [])

  const clear = useCallback(() => {
    setSelected(new Set())
    setAnchor(null)
  }, [])

  const toggleAll = useCallback((orderedPaths: string[]) => {
    setSelected((prev) => {
      // Master-checkbox semantics: only a full selection clears. Empty or
      // partial first selects everything; a second click then deselects.
      const allSelected = orderedPaths.length > 0 && orderedPaths.every((p) => prev.has(p))
      if (allSelected) return new Set()
      return new Set(orderedPaths)
    })
  }, [])

  const replaceSelection = useCallback((next: Set<string>) => {
    setSelected(new Set(next))
  }, [])

  const addSelection = useCallback((paths: string[]) => {
    setSelected((prev) => {
      const next = new Set(prev)
      for (const p of paths) next.add(p)
      return next
    })
  }, [])

  return { selected, anchor, handleClick, clear, toggleAll, replaceSelection, addSelection }
}
