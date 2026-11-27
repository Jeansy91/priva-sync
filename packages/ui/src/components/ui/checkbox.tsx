import * as React from 'react'
import { Check, Minus } from 'lucide-react'
import { cn } from '../../lib/utils'

export interface CheckboxProps
  extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'type' | 'size'> {
  /** Optional inline label, rendered to the right of the box. */
  label?: React.ReactNode
  /** Wrapper className. The visible box itself is not exposed — keep it consistent across the app. */
  wrapperClassName?: string
  /**
   * Tri-state. Renders a Minus icon and sets the DOM `indeterminate` flag;
   * `checked` is independent and controlled by the parent (e.g. true once
   * the user resolves the mixed state by clicking).
   */
  indeterminate?: boolean
}

/**
 * Projektweite Checkbox. Bewusst nicht von Radix abgeleitet, weil wir nur
 * den nativen Input brauchen — das visuelle Quadrat liegt als gestyltes
 * `<span>` darüber. Vorteil: Tastatur, Form-Integration und a11y kommen
 * geschenkt vom Browser, ohne Listener-Boilerplate.
 *
 * Höhe richtet sich nach Select/Input (h-5 → passt in eine 36-px-Zeile
 * vertikal zentriert).
 */
export const Checkbox = React.forwardRef<HTMLInputElement, CheckboxProps>(
  ({ className, wrapperClassName, label, disabled, indeterminate, ...props }, ref) => {
    // `indeterminate` is not an HTML attribute, only DOM state — set it
    // imperatively whenever the ref or the flag changes.
    const innerRef = React.useRef<HTMLInputElement | null>(null)
    React.useImperativeHandle(ref, () => innerRef.current as HTMLInputElement)
    React.useEffect(() => {
      if (innerRef.current) innerRef.current.indeterminate = !!indeterminate
    }, [indeterminate])

    const box = (
      <span className="relative inline-flex shrink-0">
        <input
          ref={innerRef}
          type="checkbox"
          disabled={disabled}
          className={cn(
            'peer h-5 w-5 cursor-pointer appearance-none rounded-sm border border-input bg-background transition-colors',
            'checked:border-primary checked:bg-primary',
            indeterminate && 'border-primary bg-primary',
            'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring/30',
            'disabled:cursor-not-allowed disabled:opacity-50',
            className,
          )}
          {...props}
        />
        {indeterminate ? (
          <Minus
            className="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 text-primary-foreground"
            strokeWidth={3}
          />
        ) : (
          <Check
            className="pointer-events-none absolute left-0.5 top-0.5 h-4 w-4 text-primary-foreground opacity-0 peer-checked:opacity-100"
            strokeWidth={3}
          />
        )}
      </span>
    )

    if (!label) return box

    return (
      <label
        className={cn(
          'inline-flex items-center gap-2 text-sm select-none',
          disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer',
          wrapperClassName,
        )}
      >
        {box}
        <span>{label}</span>
      </label>
    )
  },
)
Checkbox.displayName = 'Checkbox'
