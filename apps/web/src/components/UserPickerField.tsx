import { useEffect, useRef, useState } from 'react'
import { User as UserIcon, X, ChevronDown, Loader2 } from 'lucide-react'
import { Input, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { UserSummary } from '@privasync/api-client'
import { apiClient } from '../store/auth'

export interface UserPickerFieldProps {
  selected: UserSummary | null
  onSelect: (user: UserSummary) => void
  onClear:  () => void
  // User ids to hide from the results — e.g. recipients already granted access,
  // so the same person can't be added twice.
  exclude?: string[]
}

// Autocomplete picker for the recipient of a user share. Once the user has
// typed at least 2 characters, it debounces a request to /users/search; a
// selection swaps the input view for a "selected" chip with an X to clear.
export function UserPickerField({ selected, onSelect, onClear, exclude = [] }: UserPickerFieldProps) {
  const { t } = useTranslation()
  const [query, setQuery]     = useState('')
  const [results, setResults] = useState<UserSummary[]>([])
  const [open, setOpen]       = useState(false)
  const [loading, setLoading] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const debounceRef  = useRef<number | undefined>(undefined)
  const abortRef     = useRef<AbortController | null>(null)

  useEffect(() => {
    if (selected || !open) return
    if (debounceRef.current !== undefined) window.clearTimeout(debounceRef.current)
    const q = query.trim()
    setLoading(true)
    debounceRef.current = window.setTimeout(() => {
      abortRef.current?.abort()
      const ctrl = new AbortController()
      abortRef.current = ctrl
      apiClient.users.search(q, ctrl.signal)
        .then((users) => setResults(users ?? []))
        .catch(() => setResults([]))
        .finally(() => setLoading(false))
    }, q ? 200 : 0)
    return () => {
      if (debounceRef.current !== undefined) window.clearTimeout(debounceRef.current)
    }
  }, [query, selected, open])

  useEffect(() => {
    if (!open) return
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  if (selected) {
    return (
      <div className="flex w-full items-center gap-2 rounded-lg border border-input bg-background px-3 py-2 text-sm">
        <UserIcon className="h-4 w-4 text-muted-foreground shrink-0" />
        <span className="flex-1 truncate text-foreground">{selected.username}</span>
        <button
          type="button"
          onClick={onClear}
          className="text-muted-foreground hover:text-foreground shrink-0"
          aria-label={t('shares.pickerClear')}
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    )
  }

  return (
    <div ref={containerRef} className="relative">
      <Input
        type="text"
        value={query}
        onChange={(e) => { setQuery(e.target.value); setOpen(true) }}
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        autoComplete="off"
        placeholder={t('shares.targetUserPlaceholder')}
        className="h-auto rounded-lg px-3 py-2 pr-8 shadow-none"
      />
      <ChevronDown className={cn(
        'pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground transition-transform',
        open && 'rotate-180',
      )} />
      {open && (
        <div className="absolute left-0 right-0 top-full mt-1 z-30 max-h-48 overflow-y-auto rounded-lg border border-border bg-popover py-1 shadow-lg">
          {loading ? (
            <div className="flex items-center justify-center py-3 text-muted-foreground gap-2">
              <Loader2 className="h-4 w-4 animate-spin" />
              <span className="text-xs">{t('shares.loading')}</span>
            </div>
          ) : results.filter((u) => !exclude.includes(u.id)).length === 0 ? (
            <p className="px-3 py-2 text-sm text-muted-foreground">{t('shares.pickerNoUsers')}</p>
          ) : (
            results.filter((u) => !exclude.includes(u.id)).map((u) => (
              <button
                key={u.id}
                type="button"
                onClick={() => { onSelect(u); setOpen(false); setQuery('') }}
                className="flex items-center gap-2 w-full px-3 py-2 text-sm text-popover-foreground hover:bg-accent text-left"
              >
                <UserIcon className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                <span className="truncate">{u.username}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  )
}
