// editorMode maps a file name to the Monaco language id used for syntax
// highlighting, and decides whether a file is editable text at all. Kept as a
// pure module (no Monaco import) so the dispatch is unit-testable without a DOM.

// Extension → Monaco language id. Extensions absent here but present in
// TEXT_ONLY still open as editable plaintext (see isEditableText).
const LANG: Record<string, string> = {
  ts:   'typescript',
  tsx:  'typescript',
  js:   'javascript',
  jsx:  'javascript',
  mjs:  'javascript',
  cjs:  'javascript',
  json: 'json',
  md:   'markdown',
  markdown: 'markdown',
  yaml: 'yaml',
  yml:  'yaml',
  toml: 'ini',   // Monaco has no toml grammar; ini is the closest fit.
  sh:   'shell',
  bash: 'shell',
  zsh:  'shell',
  py:   'python',
  go:   'go',
  rs:   'rust',
  html: 'html',
  htm:  'html',
  css:  'css',
  scss: 'scss',
  less: 'less',
  xml:  'xml',
  svg:  'xml',
  sql:  'sql',
  ini:  'ini',
  conf: 'ini',
}

// Text extensions without a dedicated grammar — still editable, plaintext mode.
const TEXT_ONLY = new Set([
  'txt', 'log', 'text', 'csv', 'tsv', 'env', 'gitignore', 'dockerignore',
  'editorconfig', 'properties', 'cfg', 'list',
])

const PLAINTEXT = 'plaintext'

function ext(name: string): string | null {
  const dot = name.lastIndexOf('.')
  if (dot < 0 || dot === name.length - 1) return null
  return name.slice(dot + 1).toLowerCase()
}

// editorLanguage returns the Monaco language id for a file name. Editable
// files with no specific grammar (and the no-extension fallback) get
// 'plaintext'. Returns null only for names that are not editable text.
export function editorLanguage(name: string): string | null {
  if (!isEditableText(name)) return null
  const e = ext(name)
  if (e === null) return PLAINTEXT
  return LANG[e] ?? PLAINTEXT
}

// isEditableText decides whether the in-browser editor should offer to open a
// file. Known code/config grammars and common plaintext extensions qualify.
// Dotfiles without an extension (e.g. ".env", ".gitignore") are matched by
// their bare name. Everything else (binaries, office docs, media) is rejected.
export function isEditableText(name: string): boolean {
  const e = ext(name)
  if (e !== null) return e in LANG || TEXT_ONLY.has(e)
  // No extension: treat known dotfile basenames as text.
  const base = name.startsWith('.') ? name.slice(1).toLowerCase() : name.toLowerCase()
  return TEXT_ONLY.has(base)
}
