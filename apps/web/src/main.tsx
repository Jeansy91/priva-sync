import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { setupI18n } from '@privasync/i18n'
import { initTheme } from '@privasync/ui'
import { bootLanguageCandidates, STORED_LANG_KEY } from './i18n/bootLanguage'
import './index.css'
import App from './App'

// Pre-login: localStorage takes precedence, then browser locales.
// After login auth.ts switches to user.preferred_language.
const stored = (() => {
  try { return localStorage.getItem(STORED_LANG_KEY) } catch { return null }
})()
setupI18n(bootLanguageCandidates(
  stored,
  [...navigator.languages],
  navigator.language ?? '',
))

// Apply the persisted theme before first paint to avoid a light flash.
initTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
)
