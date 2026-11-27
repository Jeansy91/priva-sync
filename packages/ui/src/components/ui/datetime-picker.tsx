import { useEffect, useMemo, useRef, useState } from 'react'
import { Calendar as CalendarIcon, ChevronLeft, ChevronRight, X } from 'lucide-react'
import { cn } from '../../lib/utils'

/**
 * Optional overrides for the three free-form phrases the picker shows.
 * Weekday and month names are derived from the active `locale` via
 * `Intl.DateTimeFormat` and never need to be passed in.
 */
export interface DateTimePickerLabels {
  today?:       string
  clear?:       string
  placeholder?: string
}

export interface DateTimePickerProps {
  /** Local datetime as "YYYY-MM-DDTHH:mm" or "" for empty. */
  value:        string
  onChange:     (value: string) => void
  placeholder?: string
  disabled?:    boolean
  /** Show the clear-button in the popover when a value is set. Default true. */
  clearable?:   boolean
  className?:   string
  /** Override individual phrases. Anything left undefined falls back to the locale dictionary. */
  labels?:      DateTimePickerLabels
  /**
   * BCP-47 locale tag (e.g. "de-DE", "en-US"). Drives both the trigger
   * display format and the weekday/month names inside the popover.
   * Defaults to the browser's navigator.language, or "en-US" off-DOM.
   */
  locale?:      string
  /** Attached to the trigger button so external `<Label htmlFor>` works. */
  id?:          string
}

/**
 * Built-in dictionary for the three phrases that can't be derived from
 * `Intl`. Keyed by language tag (BCP-47 primary subtag). Lookup falls
 * back to "en" when the tag isn't known. Callers can still override any
 * entry via the `labels` prop.
 */
const PHRASES: Record<string, Required<DateTimePickerLabels>> = {
  en: { today: 'Today',  clear: 'Clear',  placeholder: 'Pick date & time…' },
  de: { today: 'Heute',  clear: 'Leeren', placeholder: 'Datum & Zeit wählen…' },
}

function defaultLocale(): string {
  if (typeof navigator !== 'undefined' && navigator.language) return navigator.language
  return 'en-US'
}

function phrasesFor(locale: string): Required<DateTimePickerLabels> {
  const primary = locale.toLowerCase().split('-')[0]
  return PHRASES[primary] ?? PHRASES.en
}

const pad = (n: number) => String(n).padStart(2, '0')

function parseValue(s: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(s)
  if (!m) return null
  return new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5])
}

function formatValue(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
}

/** Mon-based weekday index (Mon=0..Sun=6). */
function dowMon(d: Date): number {
  return (d.getDay() + 6) % 7
}

/**
 * Date/time picker with a modern popover style, compatible with the native
 * `<input type="datetime-local">` (same string format, local time).
 */
export function DateTimePicker({
  value,
  onChange,
  placeholder,
  disabled = false,
  clearable = true,
  className,
  labels,
  locale,
  id,
}: DateTimePickerProps) {
  const effectiveLocale = locale ?? defaultLocale()
  const phrases = phrasesFor(effectiveLocale)
  const today_      = labels?.today       ?? phrases.today
  const clear_      = labels?.clear       ?? phrases.clear
  const placeholder_ = placeholder ?? labels?.placeholder ?? phrases.placeholder

  // Weekday + month names derived from Intl — no translation table needed.
  // Anchored on known Mon..Sun and Jan..Dec dates so the formatter just labels them.
  const weekdayLabels = useMemo(() => {
    const fmt = new Intl.DateTimeFormat(effectiveLocale, { weekday: 'short' })
    // 2024-01-01 is a Monday (UTC) — pick local dates anchored on that week.
    return [1, 2, 3, 4, 5, 6, 7].map((day) => fmt.format(new Date(2024, 0, day)))
  }, [effectiveLocale])

  const monthLabels = useMemo(() => {
    const fmt = new Intl.DateTimeFormat(effectiveLocale, { month: 'long' })
    return Array.from({ length: 12 }, (_, m) => fmt.format(new Date(2024, m, 1)))
  }, [effectiveLocale])

  const displayFormatter = useMemo(
    () => new Intl.DateTimeFormat(effectiveLocale, {
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit',
    }),
    [effectiveLocale],
  )

  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const selected = useMemo(() => parseValue(value), [value])

  // Currently visible month in the popover. Follows the selection when it changes externally.
  const [view, setView] = useState<Date>(() => {
    const base = selected ?? new Date()
    return new Date(base.getFullYear(), base.getMonth(), 1)
  })

  useEffect(() => {
    if (selected) setView(new Date(selected.getFullYear(), selected.getMonth(), 1))
  }, [value])

  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const displayLabel = selected ? displayFormatter.format(selected) : placeholder_

  // Calendar grid: 6 rows × 7 columns, Mon-first, padded with prev/next month days.
  const cells = useMemo(() => {
    const first = new Date(view.getFullYear(), view.getMonth(), 1)
    const start = new Date(first)
    start.setDate(first.getDate() - dowMon(first))
    const arr: Date[] = []
    for (let i = 0; i < 42; i++) {
      const d = new Date(start)
      d.setDate(start.getDate() + i)
      arr.push(d)
    }
    return arr
  }, [view])

  const today = new Date()
  const hour   = selected ? selected.getHours()   : 12
  const minute = selected ? selected.getMinutes() : 0

  const commit = (d: Date) => onChange(formatValue(d))

  const pickDay = (d: Date) => {
    const next = new Date(d.getFullYear(), d.getMonth(), d.getDate(), hour, minute)
    commit(next)
  }

  const setHour = (h: number) => {
    const base = selected ?? new Date(view.getFullYear(), view.getMonth(), 1, 0, 0)
    const next = new Date(base)
    next.setHours(Math.max(0, Math.min(23, h)))
    commit(next)
  }

  const setMinute = (m: number) => {
    const base = selected ?? new Date(view.getFullYear(), view.getMonth(), 1, hour, 0)
    const next = new Date(base)
    next.setMinutes(Math.max(0, Math.min(59, m)))
    commit(next)
  }

  const jumpToday = () => {
    const now = new Date()
    setView(new Date(now.getFullYear(), now.getMonth(), 1))
    commit(now)
  }

  return (
    <div ref={ref} className={cn('relative w-full', className)}>
      {/*
        Hidden native input mirrors the current value so that:
          * `<Label htmlFor={id}>` resolves via `getByLabelText`
          * automated tests can read `.value` and assert/simulate input
        Screen readers ignore it (aria-hidden); the visible trigger below
        remains the primary affordance.
      */}
      <input
        id={id}
        type="datetime-local"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        aria-hidden="true"
        tabIndex={-1}
        className="sr-only"
      />
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
        <span className={cn('min-w-0 flex-1 truncate', selected ? 'text-foreground' : 'text-muted-foreground')}>{displayLabel}</span>
        <CalendarIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
      </button>

      {open && (
        <div className="absolute z-50 mt-1 w-[18rem] rounded-lg border border-border bg-popover p-3 shadow-lg">
          {/* Month navigation */}
          <div className="flex items-center justify-between pb-2">
            <button
              type="button"
              onClick={() => setView(new Date(view.getFullYear(), view.getMonth() - 1, 1))}
              className="rounded-md p-1 text-muted-foreground hover:bg-accent"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            <div className="text-sm font-medium text-foreground">
              {monthLabels[view.getMonth()]} {view.getFullYear()}
            </div>
            <button
              type="button"
              onClick={() => setView(new Date(view.getFullYear(), view.getMonth() + 1, 1))}
              className="rounded-md p-1 text-muted-foreground hover:bg-accent"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>

          {/* Weekdays */}
          <div className="grid grid-cols-7 gap-1 pb-1 text-center text-xs text-muted-foreground">
            {weekdayLabels.map((w) => <div key={w}>{w}</div>)}
          </div>

          {/* Days */}
          <div className="grid grid-cols-7 gap-1">
            {cells.map((d) => {
              const inMonth = d.getMonth() === view.getMonth()
              const isSel   = selected ? sameDay(d, selected) : false
              const isToday = sameDay(d, today)
              return (
                <button
                  key={d.toISOString()}
                  type="button"
                  onClick={() => pickDay(d)}
                  className={cn(
                    'h-8 rounded-md text-xs transition-colors',
                    !inMonth && 'text-muted-foreground/40',
                    inMonth && !isSel && 'text-foreground hover:bg-accent',
                    isSel && 'bg-primary text-primary-foreground',
                    !isSel && isToday && 'ring-1 ring-ring/50',
                  )}
                >
                  {d.getDate()}
                </button>
              )
            })}
          </div>

          {/* Time */}
          <div className="mt-3 flex items-center gap-2 border-t border-border pt-3">
            <input
              type="number"
              min={0}
              max={23}
              value={pad(hour)}
              onChange={(e) => setHour(parseInt(e.target.value, 10) || 0)}
              className="w-14 rounded-md border border-input bg-background px-2 py-1 text-center text-sm text-foreground focus:border-ring focus:outline-hidden focus:ring-2 focus:ring-ring/20"
            />
            <span className="text-muted-foreground">:</span>
            <input
              type="number"
              min={0}
              max={59}
              value={pad(minute)}
              onChange={(e) => setMinute(parseInt(e.target.value, 10) || 0)}
              className="w-14 rounded-md border border-input bg-background px-2 py-1 text-center text-sm text-foreground focus:border-ring focus:outline-hidden focus:ring-2 focus:ring-ring/20"
            />

            <div className="ml-auto flex items-center gap-1">
              <button
                type="button"
                onClick={jumpToday}
                className="rounded-md px-2 py-1 text-xs text-primary hover:bg-accent"
              >
                {today_}
              </button>
              {clearable && selected && (
                <button
                  type="button"
                  onClick={() => { onChange(''); setOpen(false) }}
                  className="rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                  aria-label={clear_}
                  title={clear_}
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
