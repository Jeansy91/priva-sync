import { ChevronRight, Home } from 'lucide-react'
import { cn } from '@privasync/ui'
import type { SyncRoot } from '@privasync/api-client'
import { pathSegments } from './helpers'
import type { FilesDnd } from './filesViewTypes'

export function FullBreadcrumbs({
  root,
  path,
  onNavigateRoot,
  onNavigatePath,
  dnd,
}: {
  root:           SyncRoot | null
  path:           string
  onNavigateRoot: () => void
  onNavigatePath: (p: string) => void
  // Each crumb (root + segments) is a move drop target.
  dnd?:           Pick<FilesDnd, 'dropProps' | 'isDropActive'>
}) {
  const segments = root ? pathSegments(path) : []
  return (
    <nav className="flex items-center gap-1 text-sm min-w-0 overflow-x-auto">
      <button
        onClick={onNavigateRoot}
        className="flex items-center gap-1 text-muted-foreground hover:text-foreground transition-colors shrink-0"
      >
        <Home className="h-3.5 w-3.5" />
      </button>
      {root && (
        <span className="flex items-center gap-1 shrink-0">
          <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
          <button
            onClick={() => onNavigatePath('/')}
            {...(dnd ? dnd.dropProps('/') : {})}
            className={cn(
              'rounded-sm px-1 hover:text-foreground transition-colors',
              path === '/' ? 'text-foreground font-medium' : 'text-muted-foreground',
              dnd?.isDropActive('/') && 'bg-primary/15 ring-1 ring-primary/50 text-foreground',
            )}
          >
            {root.name}
          </button>
        </span>
      )}
      {segments.map((seg) => (
        <span key={seg.path} className="flex items-center gap-1 shrink-0">
          <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
          <button
            onClick={() => onNavigatePath(seg.path)}
            {...(dnd ? dnd.dropProps(seg.path) : {})}
            className={cn(
              'rounded-sm px-1 hover:text-foreground transition-colors',
              seg.path === path ? 'text-foreground font-medium' : 'text-muted-foreground',
              dnd?.isDropActive(seg.path) && 'bg-primary/15 ring-1 ring-primary/50 text-foreground',
            )}
          >
            {seg.label}
          </button>
        </span>
      ))}
    </nav>
  )
}
