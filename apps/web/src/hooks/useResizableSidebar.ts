import { useCallback, useState } from 'react'

const STEP_DEFAULT = 8
const STEP_SHIFT   = 32

export interface ResizableSidebar {
  width: number
  startResize: (e: React.MouseEvent) => void
  onKeyDown:   (e: React.KeyboardEvent) => void
  min: number
  max: number
}

/**
 * Drag- + keyboard-resizable sidebar width with localStorage persistence.
 * Width is clamped to [min, max] on load and during every interaction.
 */
export function useResizableSidebar(opts: {
  storageKey:    string
  min:           number
  max:           number
  defaultWidth:  number
}): ResizableSidebar {
  const { storageKey, min, max, defaultWidth } = opts

  const clamp = useCallback((w: number) => Math.min(max, Math.max(min, w)), [min, max])

  const load = (): number => {
    try {
      const raw = localStorage.getItem(storageKey)
      const n   = raw ? parseInt(raw, 10) : NaN
      return Number.isFinite(n) ? clamp(n) : defaultWidth
    } catch {
      return defaultWidth
    }
  }

  const persist = (w: number) => {
    try { localStorage.setItem(storageKey, String(w)) } catch {}
  }

  const [width, setWidth] = useState<number>(load)

  const startResize = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const startX = e.clientX
    const startW = width
    let   latest = startW
    const onMove = (ev: MouseEvent) => {
      latest = clamp(startW + (ev.clientX - startX))
      setWidth(latest)
    }
    const onUp = () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      persist(latest)
    }
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
  }, [width, clamp])

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    const step = e.shiftKey ? STEP_SHIFT : STEP_DEFAULT
    const delta = e.key === 'ArrowLeft' ? -step : step
    setWidth((w) => {
      const next = clamp(w + delta)
      persist(next)
      return next
    })
  }

  return { width, startResize, onKeyDown, min, max }
}
