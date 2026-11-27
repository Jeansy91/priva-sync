import { ChevronDown, ChevronUp, ChevronsUpDown } from 'lucide-react'
import { cn } from '@privasync/ui'
import type { SortDir } from '../hooks/useTableSort'

export interface SortableThProps {
  label: string
  active: boolean
  dir: SortDir | null
  onToggle: () => void
  className?: string
  align?: 'left' | 'right'
}

export function SortableTh({
  label,
  active,
  dir,
  onToggle,
  className,
  align = 'left',
}: SortableThProps) {
  const Icon = active ? (dir === 'desc' ? ChevronDown : ChevronUp) : ChevronsUpDown
  return (
    <th className={className}>
      <button
        type="button"
        onClick={onToggle}
        className={cn(
          'inline-flex items-center gap-1 font-medium hover:text-foreground transition-colors',
          align === 'right' && 'flex-row-reverse',
          active && 'text-foreground',
        )}
        aria-sort={active ? (dir === 'asc' ? 'ascending' : 'descending') : 'none'}
      >
        <span>{label}</span>
        <Icon className="h-3.5 w-3.5 opacity-70" aria-hidden />
      </button>
    </th>
  )
}
