// Conflict-resolve strategy picker (ADR-0003). Headless and i18n-free — the
// caller supplies each option's label/hint text and busy/disabled state;
// this only renders the row of choices and reports which one was picked.
// No domain types: `key` is caller-defined (web/desktop both use their own
// ResolveStrategy union as the key).

import { CheckCircle, Loader2 } from 'lucide-react'

export interface ConflictStrategyOption {
  key:   string
  label: string
  hint:  string
  busy:  boolean
}

export interface ConflictStrategyChooserProps {
  options:  ConflictStrategyOption[]
  disabled?: boolean
  onSelect: (key: string) => void
}

export function ConflictStrategyChooser({ options, disabled = false, onSelect }: ConflictStrategyChooserProps) {
  return (
    <div className="space-y-2">
      {options.map((opt) => (
        <StrategyButton
          key={opt.key}
          label={opt.label}
          hint={opt.hint}
          busy={opt.busy}
          disabled={disabled}
          onClick={() => onSelect(opt.key)}
        />
      ))}
    </div>
  )
}

function StrategyButton({
  label,
  hint,
  busy,
  disabled,
  onClick,
}: {
  label:    string
  hint:     string
  busy:     boolean
  disabled: boolean
  onClick:  () => void
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      className="w-full flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2.5
                 text-left hover:bg-muted/50 disabled:opacity-50 transition-colors"
    >
      <span className="min-w-0">
        <span className="block text-sm font-medium text-foreground">{label}</span>
        <span className="block text-xs text-muted-foreground">{hint}</span>
      </span>
      {busy
        ? <Loader2 className="h-4 w-4 shrink-0 animate-spin" />
        : <CheckCircle className="h-4 w-4 shrink-0 text-muted-foreground" />}
    </button>
  )
}
