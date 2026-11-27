import { type ReactNode } from 'react'
import { cn } from '@privasync/ui'

// Native HTML tooltips (title attribute) do not fire on disabled buttons:
// Chromium suppresses pointer-events on disabled form controls and the shadcn
// Button additionally sets `disabled:pointer-events-none`. Wrapping the button
// in a <span> with the same title keeps the tooltip visible while disabled.
export function TitleWrap({
  title,
  children,
  className,
}: {
  title: string
  children: ReactNode
  className?: string
}) {
  return (
    <span title={title} className={cn('inline-flex', className)}>
      {children}
    </span>
  )
}
