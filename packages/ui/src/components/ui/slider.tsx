import * as React from 'react'
import { cn } from '../../lib/utils'

export interface SliderProps {
  value:    number
  min:      number
  max:      number
  step?:    number
  onChange: (value: number) => void
  /** Greys out the value readout — useful while a save is in flight. */
  saving?:  boolean
  disabled?: boolean
  /** Hides the tick row under the rail. */
  hideTicks?: boolean
  className?: string
}

/**
 * Projektweiter Slider — verbindlich für integer/step-Auswahlen in
 * web + desktop (z.B. parallele Uploads). Dünner Wrapper um den nativen
 * `<input type="range">`, ergänzt um eine Wert-Anzeige rechts und eine
 * Tick-Leiste unter der Rail. Voll kontrolliert.
 */
export function Slider({
  value, min, max, step = 1, onChange, saving, disabled, hideTicks, className,
}: SliderProps) {
  const ticks: number[] = []
  for (let n = min; n <= max; n += step) ticks.push(n)

  return (
    <div className={cn('space-y-1', className)}>
      <div className="flex items-center justify-end">
        <span
          className={cn(
            'text-xs font-semibold tabular-nums w-4 text-right',
            saving ? 'text-muted-foreground' : 'text-primary',
          )}
        >
          {value}
        </span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full accent-primary cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
      />
      {!hideTicks && (
        <div className="flex justify-between text-[10px] text-muted-foreground/50 select-none">
          {ticks.map((n) => <span key={n}>{n}</span>)}
        </div>
      )}
    </div>
  )
}
