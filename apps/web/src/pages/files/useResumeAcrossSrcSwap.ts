import { useEffect, useRef, useCallback, type RefObject } from 'react'

// Swapping a <video>/<audio> element's `src` resets its playback position —
// which is exactly what happens each time useMediaStreamSrc renews a grant
// mid-playback (ADR-0067). This hook restores continuity: it tells the
// element, once the new src has loaded, to seek back to where the caller
// last reported and resume playing if it was.
//
// It doesn't know or care *why* src changed — a fresh track and a
// background renewal look identical from here except for `resetKey`: as
// long as the caller passes the same key (e.g. the file path) across a src
// change, this treats it as a continuation; a different key starts the new
// src at 0 and autoplays, matching how opening a new track already behaves.
export function useResumeAcrossSrcSwap(
  mediaRef: RefObject<HTMLMediaElement | null>,
  src: string | null,
  resetKey: string,
): (time: number, playing: boolean) => void {
  const posRef = useRef({ resetKey: '', time: 0, playing: false })

  useEffect(() => {
    const el = mediaRef.current
    if (!el || !src) return

    const isContinuation = posRef.current.resetKey === resetKey
    const seekTo    = isContinuation ? posRef.current.time    : 0
    const resumePlay = isContinuation ? posRef.current.playing : true
    posRef.current.resetKey = resetKey

    const onLoaded = () => {
      if (seekTo > 0) el.currentTime = seekTo
      if (resumePlay) el.play().catch(() => {})
    }
    el.addEventListener('loadedmetadata', onLoaded, { once: true })
    return () => el.removeEventListener('loadedmetadata', onLoaded)
  }, [mediaRef, src, resetKey])

  return useCallback((time: number, playing: boolean) => {
    posRef.current.time = time
    posRef.current.playing = playing
  }, [])
}
