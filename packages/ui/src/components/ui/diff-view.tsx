// Line-diff viewer for a conflict-resolve dialog (ADR-0003). Headless and
// i18n-free — the caller supplies the column labels and the already-computed
// line diff; this only renders it. Web and desktop both produce a line diff
// locally (LCS in TS on web, a Rust command on desktop) and pass the result
// here, so DiffLine is this package's own shape, not re-exported from either
// app's domain types.

export interface DiffLine {
  kind: 'same' | 'removed' | 'added'
  text: string
}

export interface DiffViewProps {
  lines:         DiffLine[]
  currentLabel:  string
  incomingLabel: string
}

export function DiffView({ lines, currentLabel, incomingLabel }: DiffViewProps) {
  return (
    <div className="mt-2 rounded-lg border border-border overflow-hidden">
      <div className="flex gap-4 px-3 py-1.5 bg-muted/40 text-xs text-muted-foreground">
        <span>{currentLabel}</span>
        <span>{incomingLabel}</span>
      </div>
      <pre className="max-h-64 overflow-auto text-xs leading-5 m-0">
        {lines.map((line, i) => (
          <div
            key={i}
            className={
              line.kind === 'added'
                ? 'bg-green-500/10 text-green-700 dark:text-green-300 px-3'
                : line.kind === 'removed'
                  ? 'bg-red-500/10 text-red-700 dark:text-red-300 px-3'
                  : 'px-3 text-muted-foreground'
            }
          >
            {line.text || ' '}
          </div>
        ))}
      </pre>
    </div>
  )
}
