import { describe, it, expect } from 'vitest'
import { diffLines, isProbablyText } from './lineDiff'

describe('diffLines', () => {
  it('identische Inhalte ergeben nur same-Zeilen', () => {
    expect(diffLines('a\nb\nc', 'a\nb\nc')).toEqual([
      { kind: 'same', text: 'a' },
      { kind: 'same', text: 'b' },
      { kind: 'same', text: 'c' },
    ])
  })

  it('eingefügte Zeile erscheint als added', () => {
    expect(diffLines('a\nc', 'a\nb\nc')).toEqual([
      { kind: 'same', text: 'a' },
      { kind: 'added', text: 'b' },
      { kind: 'same', text: 'c' },
    ])
  })

  it('entfernte Zeile erscheint als removed', () => {
    expect(diffLines('a\nb\nc', 'a\nc')).toEqual([
      { kind: 'same', text: 'a' },
      { kind: 'removed', text: 'b' },
      { kind: 'same', text: 'c' },
    ])
  })

  it('geänderte Zeile erscheint als removed + added', () => {
    const out = diffLines('a\nx\nc', 'a\ny\nc')
    expect(out).toContainEqual({ kind: 'removed', text: 'x' })
    expect(out).toContainEqual({ kind: 'added', text: 'y' })
    expect(out.filter((l) => l.kind === 'same').map((l) => l.text)).toEqual(['a', 'c'])
  })

  it('leere Eingaben funktionieren', () => {
    expect(diffLines('', '')).toEqual([{ kind: 'same', text: '' }])
    expect(diffLines('', 'a')).toContainEqual({ kind: 'added', text: 'a' })
  })
})

describe('isProbablyText', () => {
  it('akzeptiert UTF-8-Text', () => {
    expect(isProbablyText('Hallo Wörld\nZeile 2')).toBe(true)
  })

  it('lehnt Inhalte mit NUL-Bytes ab (Binärdatei)', () => {
    expect(isProbablyText('PK' + String.fromCharCode(0) + 'binary')).toBe(false)
  })
})
