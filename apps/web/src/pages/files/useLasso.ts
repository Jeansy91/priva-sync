import { useCallback, useEffect, useRef, useState } from 'react'
import { lassoRect, pathsIntersectingLasso, type Rect, type RowRect } from './lassoIntersect'

export interface UseLasso {
  rect:        Rect | null
  onMouseDown: (e: React.MouseEvent<HTMLElement>) => void
}

type Options = {
  // Resolves which row keys live inside the container. Reads DOM at mousemove
  // because virtualised lists can re-render rows under the lasso.
  collectRows: (container: HTMLElement) => RowRect[]
  onChange:    (keys: string[], additive: boolean) => void
  // When true, suppress lasso start on the given target (e.g. clicking a row).
  isRowTarget: (target: EventTarget | null) => boolean
}

export function useLasso(opts: Options): UseLasso {
  const [rect, setRect] = useState<Rect | null>(null)
  const stateRef = useRef<{
    container: HTMLElement
    start:     { x: number; y: number }
    additive:  boolean
  } | null>(null)
  const optsRef = useRef(opts)
  optsRef.current = opts

  const onMouseDown = useCallback((e: React.MouseEvent<HTMLElement>) => {
    if (e.button !== 0) return
    if (optsRef.current.isRowTarget(e.target)) return
    const container = e.currentTarget
    const box = container.getBoundingClientRect()
    const start = {
      x: e.clientX - box.left + container.scrollLeft,
      y: e.clientY - box.top  + container.scrollTop,
    }
    stateRef.current = { container, start, additive: e.ctrlKey || e.metaKey }
    setRect({ x: start.x, y: start.y, w: 0, h: 0 })
    e.preventDefault()
  }, [])

  useEffect(() => {
    function move(e: MouseEvent) {
      const st = stateRef.current
      if (!st) return
      const box = st.container.getBoundingClientRect()
      const end = {
        x: e.clientX - box.left + st.container.scrollLeft,
        y: e.clientY - box.top  + st.container.scrollTop,
      }
      const r = lassoRect(st.start, end)
      setRect(r)
      const rows = optsRef.current.collectRows(st.container)
      const hits = pathsIntersectingLasso(rows, r)
      optsRef.current.onChange(hits, st.additive)
    }
    function up() {
      stateRef.current = null
      setRect(null)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup',   up)
    return () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup',   up)
    }
  }, [])

  return { rect, onMouseDown }
}
