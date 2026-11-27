// Theme handling shared by web and desktop. Tailwind is configured with
// darkMode: 'class', so all we do is toggle `dark` on <html>. In 'system'
// mode we follow prefers-color-scheme, including live OS switches.
export type ThemePreference = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'privasync.theme'
const DARK_QUERY = '(prefers-color-scheme: dark)'

let current: ThemePreference = 'system'

function applyClass(dark: boolean): void {
  document.documentElement.classList.toggle('dark', dark)
}

function apply(pref: ThemePreference): void {
  current = pref
  applyClass(pref === 'dark' || (pref === 'system' && matchMedia(DARK_QUERY).matches))
}

/** Read the persisted preference; falls back to 'system'. */
export function getThemePreference(): ThemePreference {
  const stored = localStorage.getItem(STORAGE_KEY)
  return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system'
}

/** Apply the given theme preference to the document and persist it. */
export function setThemePreference(pref: ThemePreference): void {
  localStorage.setItem(STORAGE_KEY, pref)
  apply(pref)
}

/** Apply the persisted preference and follow OS changes. Call once at app startup. */
export function initTheme(): void {
  apply(getThemePreference())
  matchMedia(DARK_QUERY).addEventListener('change', (e) => {
    if (current === 'system') applyClass(e.matches)
  })
}
