import { describe, expect, it } from 'vitest'

// Dark-mode guard: UI surfaces must use design tokens (bg-background,
// text-foreground, bg-muted, …) instead of hardcoded Tailwind palette grays.
// Hardcoded grays render wrong in one of the two themes unless every single
// occurrence carries a matching dark: twin — tokens make that impossible to
// forget. Accent (blue) and status colors (red/green/amber) are deliberately
// not covered; they stay hardcoded, with dark: variants where needed.

// Raw-source globs over every themed workspace, resolved at transform time so
// the test needs no node:fs access. Vite requires the options inline.
const sources: Record<string, string> = {
  ...import.meta.glob('../**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }),
  ...import.meta.glob('../../../desktop/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }),
  ...import.meta.glob('../../../../packages/ui/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true }),
} as Record<string, string>

// Glob keys are relative to this file (apps/web/src/test) — rebase them onto
// the repo root so violations and the allowlist read naturally.
function toRepoPath(key: string): string {
  if (key.startsWith('../../../../packages/')) return key.replace('../../../../', '')
  if (key.startsWith('../../../desktop/')) return key.replace('../../../', 'apps/')
  return key.replace('../', 'apps/web/src/')
}

// Grayscale palette classes in any variant chain (dark:, hover:, …).
const grayPalette = /[\w:-]*-(?:zinc|gray|neutral|slate|stone)-\d+/g
// Bare bg-white (opacity-modified bg-white/N is a translucent overlay on
// media viewers and theme-independent, so it stays allowed).
const bareBgWhite = /(?<![\w/-])bg-white(?![\w/-])/g

// repo-relative path → substrings allowed there.
const allowlist: Record<string, string[]> = {
  // QR codes need a white quiet zone to scan reliably in both themes.
  'apps/web/src/pages/settings/TOTPSection.tsx': ['bg-white'],
}

describe('hardcoded colors', () => {
  it('uses design tokens instead of grayscale palette classes', () => {
    const violations: string[] = []

    for (const [key, content] of Object.entries(sources)) {
      if (/\.test\.(ts|tsx)$/.test(key)) continue
      const rel = toRepoPath(key)
      const allowed = allowlist[rel] ?? []

      for (const regex of [grayPalette, bareBgWhite]) {
        for (const match of content.matchAll(regex)) {
          if (allowed.some((a) => match[0].includes(a))) continue
          const line = content.slice(0, match.index).split('\n').length
          violations.push(`${rel}:${line} ${match[0]}`)
        }
      }
    }

    expect(violations, violations.join('\n')).toEqual([])
  })
})
