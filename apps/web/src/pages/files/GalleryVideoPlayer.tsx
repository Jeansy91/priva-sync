import { useCallback, useEffect, useRef, useState } from 'react'
import { Pause, Play, Volume2, VolumeX } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'
import { useResumeAcrossSrcSwap } from './useResumeAcrossSrcSwap'

// GalleryVideoPlayer rendert ein Video mit eigenem Steuer-Overlay im Stil
// des AudioPlayers (Play/Pause, Seek, Zeit, Volume mit Hover-Popover).
// `resetKey` (der Dateipfad) unterscheidet einen echten Datei-Wechsel von
// einem Grant-Renewal auf dieselbe Datei (ADR-0067) — nur bei letzterem wird
// die Wiedergabeposition über den src-Wechsel hinweg erhalten.
export function GalleryVideoPlayer({ src, name, resetKey }: { src: string; name: string; resetKey: string }) {
  const { t } = useTranslation()
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const trackPlayback = useResumeAcrossSrcSwap(videoRef, src, resetKey)
  const [playing,     setPlaying]     = useState(false)
  const [currentSec,  setCurrentSec]  = useState(0)
  const [durationSec, setDurationSec] = useState(0)
  const [volume,      setVolume]      = useState(() => {
    if (typeof window === 'undefined') return 1
    const v = Number(window.localStorage.getItem('privasync.videoVolume'))
    return isFinite(v) && v >= 0 && v <= 1 ? v : 1
  })
  const [lastVolume, setLastVolume] = useState(() => volume > 0 ? volume : 1)
  const [aspect, setAspect] = useState<number | null>(null)

  useEffect(() => {
    const el = videoRef.current
    if (!el) return
    el.volume = volume
    try { window.localStorage.setItem('privasync.videoVolume', String(volume)) } catch {}
  }, [volume, src])

  const togglePlay = useCallback(() => {
    const el = videoRef.current
    if (!el) return
    if (el.paused) {
      el.play().catch(() => {})
    } else {
      el.pause()
    }
  }, [])

  const onSeek = (e: React.ChangeEvent<HTMLInputElement>) => {
    const el = videoRef.current
    if (!el) return
    const v = Number(e.target.value)
    el.currentTime = v
    setCurrentSec(v)
  }

  const onVolume = (e: React.ChangeEvent<HTMLInputElement>) => {
    const v = Number(e.target.value)
    setVolume(v)
    if (v > 0) setLastVolume(v)
  }

  const toggleMute = () => {
    if (volume > 0) {
      setLastVolume(volume)
      setVolume(0)
    } else {
      setVolume(lastVolume > 0 ? lastVolume : 1)
    }
  }

  const [hovered, setHovered] = useState(false)
  const controlsVisible = hovered || !playing

  return (
    <div
      className="relative z-10 h-[95%] w-[95vw] select-none flex items-center justify-center"
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div
        className="relative max-w-full max-h-full leading-none bg-black"
        style={{ aspectRatio: aspect ?? undefined }}
      >
        <video
          ref={videoRef}
          src={src}
          aria-label={name}
          onTimeUpdate={(e) => {
            const t = e.currentTarget.currentTime
            setCurrentSec(t)
            trackPlayback(t, !e.currentTarget.paused)
          }}
          onLoadedMetadata={(e) => {
            setDurationSec(e.currentTarget.duration || 0)
            const w = e.currentTarget.videoWidth
            const h = e.currentTarget.videoHeight
            if (w > 0 && h > 0) setAspect(w / h)
          }}
          onPlay={() => { setPlaying(true); trackPlayback(videoRef.current?.currentTime ?? 0, true) }}
          onPause={() => { setPlaying(false); trackPlayback(videoRef.current?.currentTime ?? 0, false) }}
          onClick={togglePlay}
          className="block w-full h-full object-contain"
          draggable={false}
        />

      {/* Steuer-Overlay — transparent, sitzt am unteren Videorand,
          erscheint bei Hover oder wenn pausiert. */}
      <div
        className={
          'absolute inset-x-0 -bottom-px bg-linear-to-t from-black/70 via-black/40 to-transparent text-white ' +
          'transition-opacity duration-200 ' +
          (controlsVisible ? 'opacity-100' : 'opacity-0 pointer-events-none')
        }
      >
        <div className="flex items-center gap-2 sm:gap-3 px-3 sm:px-4 h-[68px]">
          <button
            type="button"
            aria-label={playing ? t('files.audioPlayerPause') : t('files.audioPlayerPlay')}
            onClick={togglePlay}
            className="rounded-full p-2 bg-primary text-primary-foreground hover:bg-primary/90 shrink-0"
          >
            {playing ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4" />}
          </button>

          <span className="text-xs text-white/80 tabular-nums w-10 text-right shrink-0">
            {fmt(currentSec)}
          </span>
          <input
            type="range"
            aria-label={t('files.audioPlayerSeek')}
            min={0}
            max={durationSec || 0}
            step={0.1}
            value={currentSec}
            onChange={onSeek}
            disabled={!durationSec}
            className="flex-1 accent-primary"
          />
          <span className="text-xs text-white/80 tabular-nums w-10 shrink-0">
            {fmt(durationSec)}
          </span>

          {/* Volume — gleicher Stil wie AudioPlayer */}
          <div className="relative group shrink-0">
            <button
              type="button"
              aria-label={volume === 0 ? t('files.audioPlayerUnmute') : t('files.audioPlayerMute')}
              onClick={toggleMute}
              className="rounded-md p-1.5 text-white/80 hover:text-white hover:bg-white/10"
            >
              {volume === 0 ? <VolumeX className="h-4 w-4" /> : <Volume2 className="h-4 w-4" />}
            </button>
            <div className="absolute bottom-full left-1/2 -translate-x-1/2 hidden group-hover:flex items-end justify-center pt-1 pb-1 z-30">
              <div className="bg-popover border border-border rounded-md shadow-md px-2 py-3">
                <input
                  type="range"
                  aria-label={t('files.audioPlayerVolume')}
                  min={0}
                  max={1}
                  step={0.01}
                  value={volume}
                  onChange={onVolume}
                  className="h-24 w-2 accent-primary"
                  style={{ writingMode: 'vertical-lr' as React.CSSProperties['writingMode'], direction: 'rtl' }}
                />
              </div>
            </div>
          </div>
        </div>
      </div>
      </div>
    </div>
  )
}

function fmt(sec: number): string {
  if (!isFinite(sec) || sec <= 0) return '0:00'
  const m = Math.floor(sec / 60)
  const s = Math.floor(sec % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}
