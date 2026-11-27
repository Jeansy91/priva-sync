// Line-based diff for the conflict resolve dialog (ADR-0003: "Diff-Ansicht
// (Text), Auswahl"). Classic LCS over lines — conflict files are capped at
// 1 MB before this runs, so the O(n*m) table stays small.

export interface DiffLine {
  kind: 'same' | 'removed' | 'added'
  text: string
}

export function diffLines(a: string, b: string): DiffLine[] {
  const aLines = a.split('\n')
  const bLines = b.split('\n')
  const n = aLines.length
  const m = bLines.length

  // LCS length table.
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = aLines[i] === bLines[j]
        ? lcs[i + 1][j + 1] + 1
        : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }

  // Backtrack: emit removed (only in a), added (only in b), same (in both).
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (aLines[i] === bLines[j]) {
      out.push({ kind: 'same', text: aLines[i] })
      i++; j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: 'removed', text: aLines[i] })
      i++
    } else {
      out.push({ kind: 'added', text: bLines[j] })
      j++
    }
  }
  for (; i < n; i++) out.push({ kind: 'removed', text: aLines[i] })
  for (; j < m; j++) out.push({ kind: 'added', text: bLines[j] })
  return out
}

// Cheap binary sniff: real text never contains NUL. Decoded UTF-8 with
// replacement characters still renders fine, so we only reject NUL.
export function isProbablyText(content: string): boolean {
  return !content.includes(String.fromCharCode(0))
}
