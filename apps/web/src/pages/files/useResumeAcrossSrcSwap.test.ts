import { describe, it, expect, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useResumeAcrossSrcSwap } from './useResumeAcrossSrcSwap'

// A stubbed <audio>/<video> element — jsdom's HTMLMediaElement throws
// "not implemented" on play(), so it's swapped for a spy. Per the ADR-0067
// design, this hook's contract is "react to a src change by reapplying
// currentTime/play-state on the element" — independent of *why* src changed
// (fresh track vs. background renewal), so a plain stub is enough; no real
// media pipeline is exercised.
function mediaStub() {
  const el = document.createElement('audio')
  el.play = vi.fn().mockResolvedValue(undefined)
  return el
}

describe('useResumeAcrossSrcSwap', () => {
  it('starts a fresh resetKey at time 0 and autoplays', () => {
    const el = mediaStub()
    const ref = { current: el }

    renderHook(({ src, key }) => useResumeAcrossSrcSwap(ref, src, key), {
      initialProps: { src: 'https://api.test/media/tok-1', key: '/clip.mp4' },
    })

    act(() => { el.dispatchEvent(new Event('loadedmetadata')) })

    expect(el.currentTime).toBe(0)
    expect(el.play).toHaveBeenCalledTimes(1)
  })

  it('resumes at the tracked position when the resetKey is unchanged (renewal)', () => {
    const el = mediaStub()
    const ref = { current: el }

    const { result, rerender } = renderHook(
      ({ src, key }) => useResumeAcrossSrcSwap(ref, src, key),
      { initialProps: { src: 'https://api.test/media/tok-1', key: '/clip.mp4' } },
    )
    act(() => { el.dispatchEvent(new Event('loadedmetadata')) })
    ;(el.play as ReturnType<typeof vi.fn>).mockClear()

    // Caller reports live playback position (e.g. from onTimeUpdate/onPlay).
    act(() => { result.current(42, true) })

    // Renewal: same resetKey, new src.
    rerender({ src: 'https://api.test/media/tok-2', key: '/clip.mp4' })
    act(() => { el.dispatchEvent(new Event('loadedmetadata')) })

    expect(el.currentTime).toBe(42)
    expect(el.play).toHaveBeenCalledTimes(1)
  })

  it('resets to a fresh start across a genuine track change, ignoring the old track\'s tracked position', () => {
    const el = mediaStub()
    const ref = { current: el }

    const { result, rerender } = renderHook(
      ({ src, key }) => useResumeAcrossSrcSwap(ref, src, key),
      { initialProps: { src: 'https://api.test/media/tok-1', key: '/song-a.mp3' } },
    )
    act(() => { el.dispatchEvent(new Event('loadedmetadata')) })
    act(() => { result.current(120, false) }) // paused deep into song A

    // Track change: different resetKey.
    rerender({ src: 'https://api.test/media/tok-b', key: '/song-b.mp3' })
    act(() => { el.dispatchEvent(new Event('loadedmetadata')) })

    expect(el.currentTime).toBe(0)
    expect(el.play).toHaveBeenCalledTimes(2) // autoplay on open, both tracks
  })
})
