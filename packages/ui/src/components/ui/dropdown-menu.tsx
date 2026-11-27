import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { MoreHorizontal } from 'lucide-react'
import { cn } from '../../lib/utils'

export interface DropdownMenuItem {
  label:     string
  icon?:     ReactNode
  onSelect:  () => void
  disabled?: boolean
  variant?:  'default' | 'destructive'
}

export interface DropdownMenuProps {
  items:        DropdownMenuItem[]
  triggerLabel: string
  trigger?:     ReactNode
  align?:       'start' | 'end'
  className?:   string
}

/**
 * Dropdown rendert das Menü via Portal an <body>, damit Container mit
 * `overflow:hidden|auto` (z.B. scrollende Tabellen) es nicht clippen.
 * Position wird aus dem Trigger-Rect berechnet; öffnet sich nach unten,
 * fliegt nach oben wenn unten kein Platz ist.
 */
export function DropdownMenu({
  items,
  triggerLabel,
  trigger,
  align = 'end',
  className,
}: DropdownMenuProps) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef    = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)

  // Position berechnen sobald offen.
  useLayoutEffect(() => {
    if (!open || !triggerRef.current) return
    const compute = () => {
      const t = triggerRef.current!.getBoundingClientRect()
      const menuWidth  = menuRef.current?.offsetWidth  ?? 192
      const menuHeight = menuRef.current?.offsetHeight ?? 200
      const vpH = window.innerHeight
      const vpW = window.innerWidth
      const spaceBelow = vpH - t.bottom
      const top = spaceBelow >= menuHeight + 8 || t.top < menuHeight + 8
        ? t.bottom + 4
        : t.top - menuHeight - 4
      let left = align === 'end' ? t.right - menuWidth : t.left
      // an Viewport klemmen
      left = Math.max(8, Math.min(left, vpW - menuWidth - 8))
      setPos({ top, left })
    }
    compute()
    window.addEventListener('resize', compute)
    window.addEventListener('scroll', compute, true)
    return () => {
      window.removeEventListener('resize', compute)
      window.removeEventListener('scroll', compute, true)
    }
  }, [open, align])

  // Click outside or Escape closes the menu.
  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      const target = e.target as Node
      if (triggerRef.current?.contains(target)) return
      if (menuRef.current?.contains(target))    return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDoc)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const menu = open && pos && typeof document !== 'undefined' ? createPortal(
    <div
      ref={menuRef}
      role="menu"
      style={{ position: 'fixed', top: pos.top, left: pos.left }}
      className="z-100 min-w-48 rounded-lg border border-border bg-popover py-1 shadow-lg"
    >
      {items.map((it) => (
        <button
          key={it.label}
          type="button"
          role="menuitem"
          disabled={it.disabled}
          onClick={() => {
            if (it.disabled) return
            setOpen(false)
            it.onSelect()
          }}
          className={cn(
            'flex w-full items-center gap-2 px-3 py-2 text-left text-sm transition-colors',
            it.disabled
              ? 'cursor-not-allowed opacity-50'
              : it.variant === 'destructive'
                ? 'text-destructive hover:bg-destructive/10'
                : 'text-foreground hover:bg-accent',
          )}
        >
          {it.icon}
          {it.label}
        </button>
      ))}
    </div>,
    document.body,
  ) : null

  return (
    <div className={cn('inline-block', className)}>
      <button
        ref={triggerRef}
        type="button"
        aria-label={triggerLabel}
        title={triggerLabel}
        onClick={() => setOpen((p) => !p)}
        className={cn(
          'inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground',
          'hover:bg-accent hover:text-accent-foreground transition-colors',
          open && 'bg-accent text-accent-foreground',
        )}
      >
        {trigger ?? <MoreHorizontal className="h-4 w-4" />}
      </button>
      {menu}
    </div>
  )
}
