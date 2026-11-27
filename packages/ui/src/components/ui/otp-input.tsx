import * as React from 'react'
import { cn } from '../../lib/utils'

export interface OtpInputProps {
  value: string
  onChange: (value: string) => void
  onComplete?: (value: string) => void
  length?: number
  disabled?: boolean
  autoFocus?: boolean
  hasError?: boolean
  className?: string
  inputClassName?: string
  ariaLabel?: string
  id?: string
  name?: string
  onBlur?: () => void
}

function OtpInput({
  value,
  onChange,
  onComplete,
  length = 6,
  disabled,
  autoFocus,
  hasError,
  className,
  inputClassName,
  ariaLabel,
  id,
  name,
  onBlur,
}: OtpInputProps) {
  const refs = React.useRef<Array<HTMLInputElement | null>>([])
  // Flag set while we move focus ourselves so handleFocus skips its
  // "first empty cell" redirect — that logic uses the digits captured by
  // the current render and would see stale state mid-event.
  const programmaticFocusRef = React.useRef(false)
  const normalized = value.replace(/\D/g, '').slice(0, length)
  const digits = Array.from({ length }, (_, i) => normalized[i] ?? '')

  const focusCell = (i: number) => {
    const el = refs.current[Math.max(0, Math.min(length - 1, i))]
    if (!el) return
    programmaticFocusRef.current = true
    el.focus()
  }

  const emit = (next: string) => {
    onChange(next)
    if (next.length === length && onComplete) onComplete(next)
  }

  const handleChange = (i: number, raw: string) => {
    const cleaned = raw.replace(/\D/g, '')
    const old = digits[i]
    let digit: string
    if (!cleaned) {
      digit = ''
    } else if (cleaned.length === 1) {
      digit = cleaned
    } else {
      // Multiple chars (user typed over a filled cell — browser appended/prepended).
      // Pick the newest char: scan from the end for one that differs from old.
      digit = [...cleaned].reverse().find((c) => c !== old) ?? cleaned.slice(-1)
    }
    const arr = digits.slice()
    arr[i] = digit
    emit(arr.join(''))
    if (digit && i < length - 1) focusCell(i + 1)
  }

  const handleKeyDown = (i: number, e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Backspace') {
      if (digits[i]) {
        const arr = digits.slice()
        arr[i] = ''
        emit(arr.join(''))
      } else if (i > 0) {
        const arr = digits.slice()
        arr[i - 1] = ''
        emit(arr.join(''))
        focusCell(i - 1)
      }
      e.preventDefault()
      return
    }
    if (e.key === 'ArrowLeft' && i > 0) {
      e.preventDefault()
      focusCell(i - 1)
      return
    }
    if (e.key === 'ArrowRight' && i < length - 1) {
      e.preventDefault()
      focusCell(i + 1)
      return
    }
    if (e.key === 'Home') {
      e.preventDefault()
      focusCell(0)
      return
    }
    if (e.key === 'End') {
      e.preventDefault()
      focusCell(length - 1)
    }
  }

  const handlePaste = (i: number, e: React.ClipboardEvent<HTMLInputElement>) => {
    const text = e.clipboardData.getData('text').replace(/\D/g, '')
    if (!text) return
    e.preventDefault()
    const arr = digits.slice()
    let cursor = i
    for (const ch of text) {
      if (cursor >= length) break
      arr[cursor++] = ch
    }
    const joined = arr.join('')
    emit(joined)
    focusCell(Math.min(cursor, length - 1))
  }

  const handleFocus = (i: number, e: React.FocusEvent<HTMLInputElement>) => {
    if (programmaticFocusRef.current) {
      programmaticFocusRef.current = false
      if (digits[i]) e.currentTarget.select()
      return
    }
    const firstEmpty = digits.findIndex((d) => !d)
    if (firstEmpty !== -1 && firstEmpty < i) {
      focusCell(firstEmpty)
      return
    }
    // Select existing content so typing replaces it; leave empty cells alone
    // so the first digit isn't swallowed as a "replacement".
    if (digits[i]) {
      e.currentTarget.select()
    }
  }

  return (
    <div
      className={cn('flex items-center gap-2', className)}
      role="group"
      aria-label={ariaLabel}
    >
      {digits.map((d, i) => (
        <input
          key={i}
          ref={(el) => { refs.current[i] = el }}
          id={i === 0 ? id : undefined}
          name={i === 0 ? name : undefined}
          type="text"
          inputMode="numeric"
          autoComplete={i === 0 ? 'one-time-code' : 'off'}
          disabled={disabled}
          autoFocus={autoFocus && i === 0}
          value={d}
          onChange={(e) => handleChange(i, e.target.value)}
          onKeyDown={(e) => handleKeyDown(i, e)}
          onPaste={(e) => handlePaste(i, e)}
          onFocus={(e) => handleFocus(i, e)}
          onBlur={i === length - 1 ? onBlur : undefined}
          aria-label={`${ariaLabel ?? 'Digit'} ${i + 1}`}
          className={cn(
            'h-12 w-10 rounded-md border bg-background text-center font-mono text-lg shadow-2xs transition-colors',
            'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring',
            'disabled:cursor-not-allowed disabled:opacity-50',
            hasError ? 'border-destructive' : 'border-input',
            inputClassName,
          )}
        />
      ))}
    </div>
  )
}

export { OtpInput }
