import { useCallback, useSyncExternalStore } from 'react'

// Tracks a CSS media query. Without matchMedia (jsdom, SSR) it reports
// `fallback`, so callers pick the layout the environment should assume.
export function useMediaQuery(query: string, fallback = false): boolean {
  const subscribe = useCallback((onChange: () => void) => {
    if (typeof window === 'undefined' || !window.matchMedia) return () => {}
    const mql = window.matchMedia(query)
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [query])
  const getSnapshot = () =>
    typeof window !== 'undefined' && window.matchMedia
      ? window.matchMedia(query).matches
      : fallback
  return useSyncExternalStore(subscribe, getSnapshot, () => fallback)
}
