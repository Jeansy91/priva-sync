import { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { cn } from '../../lib/utils'

export interface SelectOption {
  value: string
  label: string
}

export interface SelectProps {
  value:        string
  onChange:     (value: string) => void
  options:      SelectOption[]
  /** Shown when no option is selected. Caller-provided so the component stays i18n-agnostic. */
  placeholder?: string
  /** Shown inside the dropdown when `options` is empty. */
  emptyLabel?:  string
  disabled?:    boolean
  className?:   string
}

/**
 * Project-wide dropdown — mandatory for all selection lists in web +
 * desktop. Design-token colors so the component renders identically in
 * both apps and in light/dark mode.
 *
 * Fully controlled; no i18n coupling — strings come in as props.
 */
export function Select({
  value,
  onChange,
  options,
  placeholder = 'Select…',
  emptyLabel  = 'No entries',
  disabled = false,
  className,
}: SelectProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const selected = options.find((o) => o.value === value)

  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  return (
    <div ref={ref} className={cn('relative w-full', className)}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((p) => !p)}
        className={cn(
          'flex w-full items-center justify-between gap-2 rounded-lg border bg-background px-3 py-2 text-left text-sm transition-colors',
          open
            ? 'border-ring ring-2 ring-ring/20'
            : 'border-input hover:border-muted-foreground/50',
          disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer',
        )}
      >
        <span className={selected ? 'text-foreground' : 'text-muted-foreground'}>
          {selected ? selected.label : placeholder}
        </span>
        <ChevronDown
          className={cn(
            'h-4 w-4 shrink-0 text-muted-foreground transition-transform',
            open && 'rotate-180',
          )}
        />
      </button>

      {open && (
        <div className="absolute z-50 mt-1 w-full rounded-lg border border-border bg-popover py-1 shadow-lg">
          {options.length === 0 ? (
            <p className="px-3 py-2 text-sm text-muted-foreground">{emptyLabel}</p>
          ) : (
            options.map((opt) => {
              const isSel = opt.value === value
              return (
                <button
                  key={opt.value}
                  type="button"
                  onClick={() => { onChange(opt.value); setOpen(false) }}
                  className={cn(
                    'flex w-full items-center gap-2 px-3 py-2 text-sm transition-colors',
                    isSel
                      ? 'bg-accent text-accent-foreground'
                      : 'text-popover-foreground hover:bg-accent/60',
                  )}
                >
                  <Check
                    className={cn(
                      'h-3.5 w-3.5 shrink-0',
                      isSel ? 'opacity-100 text-primary' : 'opacity-0',
                    )}
                  />
                  {opt.label}
                </button>
              )
            })
          )}
        </div>
      )}
    </div>
  )
}
