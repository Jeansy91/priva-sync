import { type ReactNode } from 'react'
import { cn } from '@privasync/ui'

interface PageHeaderProps {
  title: ReactNode
  subtitle?: ReactNode
  /** Right-aligned controls; wrap below the title on narrow screens. */
  actions?: ReactNode
  /** Shows the bottom border once the content below has scrolled. */
  scrolled: boolean
  /** Must match the content column so header and body stay aligned. */
  maxWidth?: 'max-w-4xl' | 'max-w-7xl'
}

export function PageHeader({ title, subtitle, actions, scrolled, maxWidth = 'max-w-7xl' }: PageHeaderProps) {
  return (
    <div className={cn('bg-background shrink-0 transition-shadow', scrolled && 'border-b border-border')}>
      <div className={cn(
        maxWidth,
        'mx-auto flex flex-col gap-3 px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:px-6',
      )}>
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold">{title}</h1>
          {subtitle && <p className="text-sm text-muted-foreground mt-0.5">{subtitle}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2 sm:shrink-0">{actions}</div>}
      </div>
    </div>
  )
}
