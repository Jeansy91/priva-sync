import * as React from 'react'
import { cn } from '../../lib/utils'

export interface SwitchProps {
  checked:   boolean
  onChange:  (checked: boolean) => void
  disabled?: boolean
  label?:    React.ReactNode
  /** Inline-Hinweis unter/rechts vom Schalter. */
  description?: React.ReactNode
  className?: string
  /** Forwarded an den nativen `<input>` für Form-Submission und a11y. */
  name?:     string
  id?:       string
}

/**
 * Projektweiter Toggle-Switch im iOS/Material-Stil. Versteckt einen nativen
 * Checkbox-Input für Tastatur + Form-Integration; der bunte Track ist ein
 * eigenes `<span>` mit Tailwind-Transitions.
 *
 * Verwendung bevorzugt bei Booleans, die unmittelbar wirken (Live-Toggles),
 * während `Checkbox` für Einstell-Formulare gedacht ist.
 */
export const Switch = React.forwardRef<HTMLInputElement, SwitchProps>(
  ({ checked, onChange, disabled, label, description, className, name, id }, ref) => {
    const inputId = id ?? React.useId()
    const track = (
      <span className="relative inline-flex h-5 w-9 shrink-0 items-center">
        <input
          ref={ref}
          id={inputId}
          name={name}
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(e) => onChange(e.target.checked)}
          className="peer sr-only"
        />
        <span
          aria-hidden
          className={cn(
            'absolute inset-0 rounded-full transition-colors',
            checked ? 'bg-primary' : 'bg-muted-foreground/30',
            disabled && 'opacity-50',
            'peer-focus-visible:ring-2 peer-focus-visible:ring-ring/30',
          )}
        />
        <span
          aria-hidden
          className={cn(
            'absolute h-4 w-4 rounded-full bg-background shadow-xs transition-transform',
            checked ? 'translate-x-4' : 'translate-x-0.5',
          )}
        />
      </span>
    )

    const inner = label || description
      ? (
        <span className="flex flex-col">
          {label && <span className="text-sm">{label}</span>}
          {description && <span className="text-xs text-muted-foreground">{description}</span>}
        </span>
      )
      : null

    return (
      <label
        htmlFor={inputId}
        className={cn(
          'inline-flex items-center gap-3 select-none',
          disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer',
          className,
        )}
      >
        {track}
        {inner}
      </label>
    )
  },
)
Switch.displayName = 'Switch'
