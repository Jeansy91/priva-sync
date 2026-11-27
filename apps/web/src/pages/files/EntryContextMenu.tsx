import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from '@privasync/i18n'
import { cn } from '@privasync/ui'
import type { DirEntry } from './helpers'
import { buildEntryMenuItems, type EntryMenuCallbacks } from './entryMenuItems'

export type ContextMenuAnchor = { x: number; y: number }

type Props = {
  anchor:    ContextMenuAnchor | null
  entries:   DirEntry[]
  callbacks: EntryMenuCallbacks
  onClose:   () => void
}

// Right-click menu for file/dir rows. Renders into a portal so overflow:hidden
// on the scrolling table doesn't clip it; flips to stay inside the viewport
// when the cursor is near the right or bottom edge.
export function EntryContextMenu({ anchor, entries, callbacks, onClose }: Props) {
  const { t } = useTranslation()
  const menuRef = useRef<HTMLDivElement | null>(null)
  const [pos, setPos] = useState<ContextMenuAnchor | null>(anchor)

  useLayoutEffect(() => {
    if (!anchor || !menuRef.current) {
      setPos(anchor)
      return
    }
    const rect = menuRef.current.getBoundingClientRect()
    const vw = window.innerWidth
    const vh = window.innerHeight
    let x = anchor.x
    let y = anchor.y
    if (x + rect.width  > vw) x = Math.max(0, vw - rect.width  - 4)
    if (y + rect.height > vh) y = Math.max(0, vh - rect.height - 4)
    setPos({ x, y })
  }, [anchor])

  useEffect(() => {
    if (!anchor) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); onClose() }
    }
    const onDown = (e: MouseEvent) => {
      if (!menuRef.current) { onClose(); return }
      if (!menuRef.current.contains(e.target as Node)) onClose()
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener('mousedown', onDown)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('mousedown', onDown)
    }
  }, [anchor, onClose])

  if (!anchor) return null
  const items = buildEntryMenuItems(entries, callbacks, t)
  if (items.length === 0) return null

  const style: React.CSSProperties = pos
    ? { position: 'fixed', left: pos.x, top: pos.y, zIndex: 50 }
    : { position: 'fixed', left: -9999, top: -9999, zIndex: 50, visibility: 'hidden' }

  return createPortal(
    <div
      ref={menuRef}
      role="menu"
      style={style}
      className="min-w-48 rounded-md border border-border bg-popover p-1 text-sm shadow-md"
    >
      {items.map((item, i) => (
        <button
          key={i}
          type="button"
          role="menuitem"
          disabled={item.disabled}
          onClick={() => { item.onSelect(); onClose() }}
          className={cn(
            'flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left hover:bg-accent disabled:opacity-50',
            item.variant === 'destructive' && 'text-destructive hover:bg-destructive/10',
          )}
        >
          {item.icon}
          <span>{item.label}</span>
        </button>
      ))}
    </div>,
    document.body,
  )
}
