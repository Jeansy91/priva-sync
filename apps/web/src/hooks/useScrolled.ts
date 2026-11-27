import { useState, type UIEvent } from 'react'

/**
 * Tracks whether a scroll container has been scrolled past the top.
 * Returned `onScroll` is meant to be passed to the scroll-host div;
 * `scrolled` flips to `true` as soon as `scrollTop > 0`.
 *
 * Wird genutzt, damit Page-Header ihren Bottom-Border erst zeigen,
 * sobald Inhalt darunter wegscrollt — in Ruhelage „klebt" der Header
 * optisch am Inhalt, kein doppelter Trenner.
 */
export function useScrolled() {
  const [scrolled, setScrolled] = useState(false)
  const onScroll = (e: UIEvent<HTMLDivElement>) => {
    const next = e.currentTarget.scrollTop > 0
    setScrolled((prev) => (prev === next ? prev : next))
  }
  return { scrolled, onScroll }
}
