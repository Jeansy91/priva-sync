import { useEffect, useRef, useState } from 'react'
import { mediaStreamUrlFrom, type FileSource } from './fileSource'

export type MediaStreamState = { src: string | null; error: boolean }

// Mints a media-stream grant (ADR-0067) for `path` and keeps it fresh: the
// grant has a short TTL so an outstanding token can't outlive a logout or
// share revocation for long, but a player can run far longer than that TTL
// (a movie, a long podcast) — so this hook re-mints in the background near
// expiry and hands the caller a fresh `src` without being asked. Renewal
// swaps the DOM element's src, which resets its playback position; pairing
// this with useResumeAcrossSrcSwap on the caller's <audio>/<video> element
// restores continuity across that swap.
export function useMediaStreamSrc(source: FileSource | null, path: string | null): MediaStreamState {
  const [state, setState] = useState<MediaStreamState>({ src: null, error: false })
  const renewalTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (renewalTimer.current) clearTimeout(renewalTimer.current)
    setState({ src: null, error: false })
    if (!source || !path) return

    let cancelled = false

    const mint = (isInitial: boolean) => {
      mediaStreamUrlFrom(source, path)
        .then(({ url, expiresIn }) => {
          if (cancelled) return
          setState({ src: url, error: false })
          // Renew at 75% of the grant's TTL — long before it actually
          // lapses, so a slow renewal never races an expiring token.
          renewalTimer.current = setTimeout(() => mint(false), expiresIn * 0.75 * 1000)
        })
        .catch(() => {
          if (cancelled) return
          if (isInitial) {
            // Never had a working src — surface the failure.
            setState({ src: null, error: true })
            return
          }
          // A working src is already playing; a renewal hiccup shouldn't
          // kill it. Retry soon — there's still runway left on the old
          // grant before its own TTL lapses.
          renewalTimer.current = setTimeout(() => mint(false), 30 * 1000)
        })
    }
    mint(true)

    return () => {
      cancelled = true
      if (renewalTimer.current) clearTimeout(renewalTimer.current)
    }
  }, [source, path])

  return state
}
