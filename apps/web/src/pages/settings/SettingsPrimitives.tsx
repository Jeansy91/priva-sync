import { cn } from '@privasync/ui'
import { CheckCircle, AlertCircle } from 'lucide-react'

/**
 * Inline alert strip shared across all settings sections. Two variants:
 * success (green) and error (destructive). Renders the status icon
 * automatically.
 */
export function Alert({
  type,
  message,
}: {
  type: 'success' | 'error'
  message: string
}) {
  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-md border px-3 py-2 text-sm',
        type === 'success'
          ? 'border-green-500/40 bg-green-50/30 text-green-700 dark:text-green-400'
          : 'border-destructive/50 bg-destructive/10 text-destructive'
      )}
    >
      {type === 'success'
        ? <CheckCircle className="h-4 w-4 shrink-0" />
        : <AlertCircle className="h-4 w-4 shrink-0" />}
      {message}
    </div>
  )
}

/**
 * Card wrapper for a single concern within a settings group (e.g.
 * "Password change", "TOTP"). Title row plus optional description and a
 * leading icon chip.
 */
export function SectionCard({
  title,
  description,
  icon,
  children,
}: {
  title: string
  description?: string
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="rounded-xl border border-border bg-card p-4 sm:p-5 space-y-4 shadow-xs">
      <div className="flex items-start gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-muted text-muted-foreground shrink-0">
          {icon}
        </div>
        <div className="min-w-0">
          <h3 className="font-semibold leading-tight">{title}</h3>
          {description && (
            <p className="text-sm text-muted-foreground mt-1">{description}</p>
          )}
        </div>
      </div>
      {children}
    </div>
  )
}

/**
 * Top-level grouping wrapper that bundles several SectionCards under a
 * shared heading (e.g. "Account", "Security", "Appearance"). Uses a
 * larger icon chip in the primary accent colour.
 */
export function SettingsGroup({
  title,
  description,
  icon,
  children,
}: {
  title: string
  description?: string
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section className="rounded-2xl border border-border bg-muted/30 p-3 sm:p-6 space-y-4 sm:space-y-5">
      <div className="flex items-start gap-3">
        <div className="flex h-11 w-11 items-center justify-center rounded-xl bg-primary/15 text-primary shrink-0">
          {icon}
        </div>
        <div className="min-w-0">
          <h2 className="text-xl font-semibold tracking-tight leading-tight">{title}</h2>
          {description && (
            <p className="text-sm text-muted-foreground mt-1">{description}</p>
          )}
        </div>
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  )
}
