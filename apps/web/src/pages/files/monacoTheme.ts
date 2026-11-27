// monacoTheme picks the built-in Monaco theme matching the app's current
// light/dark mode. The web app toggles dark mode via a `dark` class on
// <html> (see packages/ui theme.ts), so we read that rather than a store.
export function monacoTheme(): 'vs' | 'vs-dark' {
  if (typeof document === 'undefined') return 'vs'
  return document.documentElement.classList.contains('dark') ? 'vs-dark' : 'vs'
}
