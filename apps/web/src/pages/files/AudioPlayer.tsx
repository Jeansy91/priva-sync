import { useCallback, useEffect, useRef, useState } from 'react'
import {
  AlertCircle, ChevronLeft, ChevronRight, ListMusic, Loader2, Music2,
  Pause, Play, Volume2, VolumeX, X,
} from 'lucide-react'
import { useTranslation } from '@privasync/i18n'
import { useAudioPlayer } from '../../store/audioPlayer'
import { extractCover } from './id3Cover'
import { downloadRangeFrom, mediaStreamUrlFrom, type FileSource } from './fileSource'
import { useMediaStreamSrc } from './useMediaStreamSrc'
import { useResumeAcrossSrcSwap } from './useResumeAcrossSrcSwap'

// ID3v2 tags sit at the start of the file — extractCover never reads past
// this window (id3Cover.ts), so a Range-fetch of just the first 1 MB is
// enough; no need to pull the whole track for cover art (ADR-0067).
const COVER_ART_RANGE = 'bytes=0-1048575'

// AudioPlayer ist global im AppShell gemountet — bleibt also auch beim
// Wechsel der Route bestehen. Sichtbar nur wenn der Store eine Playlist hat.
export function AudioPlayer() {
  const { t } = useTranslation()
  const visible = useAudioPlayer((s) => s.visible)
  const source  = useAudioPlayer((s) => s.source)
  const tracks  = useAudioPlayer((s) => s.tracks)
  const index   = useAudioPlayer((s) => s.index)
  const prev    = useAudioPlayer((s) => s.prev)
  const next    = useAudioPlayer((s) => s.next)
  const setIdx  = useAudioPlayer((s) => s.setIndex)
  const close   = useAudioPlayer((s) => s.close)

  const audioRef = useRef<HTMLAudioElement | null>(null)
  const [coverUrl,   setCoverUrl]   = useState<string | null>(null)
  const [playing,    setPlaying]    = useState(false)
  const [currentSec, setCurrentSec] = useState(0)
  const [durationSec, setDurationSec] = useState(0)
  const [showList, setShowList] = useState(false)
  const [volume, setVolume] = useState(() => {
    if (typeof window === 'undefined') return 1
    const v = Number(window.localStorage.getItem('privasync.audioVolume'))
    return isFinite(v) && v >= 0 && v <= 1 ? v : 1
  })
  const [lastVolume, setLastVolume] = useState(() => volume > 0 ? volume : 1)
  const [durations, setDurations] = useState<Record<string, number>>({})

  const current = tracks[index]

  // Wiedergabe streamt direkt vom Server statt vorher komplett zu laden
  // (ADR-0067) — das <audio>-Element bekommt die Grant-URL als src, Range
  // übernimmt der Browser selbst. Der Hook erneuert den Grant im Hintergrund
  // vor Ablauf; useResumeAcrossSrcSwap gleicht die dabei zurückgesetzte
  // Wiedergabeposition wieder aus.
  const { src, error: streamError } = useMediaStreamSrc(
    visible ? source : null,
    visible ? current?.path ?? null : null,
  )
  const resetKey = current?.path ?? ''
  const trackPlayback = useResumeAcrossSrcSwap(audioRef, src, resetKey)
  const error = streamError ? t('files.audioPlayerLoadFailed') : null

  // Reset per-Track-UI-State, wenn der Track selbst wechselt (nicht bei
  // einem Grant-Renewal auf denselben Track).
  useEffect(() => {
    setCoverUrl(null)
    setCurrentSec(0)
  }, [current?.path])

  // Cover-Art beste-Effort: nur die ersten 1 MB (ID3-Tag sitzt am
  // Dateianfang) statt der ganzen Datei — extractCover liest ohnehin nie
  // mehr als das (ADR-0067).
  useEffect(() => {
    if (!visible || !current || !source) return
    let revoke: string | null = null
    let cancelled = false
    ;(async () => {
      try {
        const res = await downloadRangeFrom(source, current.path, COVER_ART_RANGE)
        if (!res.ok) return
        const blob = await res.blob()
        if (cancelled) return
        const cover = await extractCover(blob)
        if (!cancelled && cover) {
          const cUrl = URL.createObjectURL(cover.blob)
          revoke = cUrl
          setCoverUrl(cUrl)
        }
      } catch { /* best effort — Icon-Fallback bleibt */ }
    })()
    return () => {
      cancelled = true
      if (revoke) URL.revokeObjectURL(revoke)
    }
  }, [visible, source, current?.path])

  const togglePlay = useCallback(() => {
    const el = audioRef.current
    if (!el) return
    if (el.paused) {
      el.play().then(() => setPlaying(true)).catch(() => {})
    } else {
      el.pause()
      setPlaying(false)
    }
  }, [])

  const onSeek = (e: React.ChangeEvent<HTMLInputElement>) => {
    const el = audioRef.current
    if (!el) return
    const v = Number(e.target.value)
    el.currentTime = v
    setCurrentSec(v)
  }

  // Volume an Audio-Element + localStorage spiegeln.
  useEffect(() => {
    const el = audioRef.current
    if (!el) return
    el.volume = volume
    try { window.localStorage.setItem('privasync.audioVolume', String(volume)) } catch {}
  }, [volume, src])

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

  // Aktuellen Track in den Durations-Cache aufnehmen, sobald metadata da ist.
  useEffect(() => {
    if (!current || durationSec <= 0) return
    setDurations((prev) => prev[current.path] === durationSec
      ? prev
      : { ...prev, [current.path]: durationSec })
  }, [current?.path, durationSec])

  // Wenn die Playlist geöffnet ist, fehlende Dauern sequenziell nachladen —
  // pro Track ein verstecktes <audio preload="metadata">, das nach erstem
  // loadedmetadata abgebrochen wird. Sequenziell, damit eine lange Playlist
  // keine 50 parallelen Downloads erzeugt.
  useEffect(() => {
    if (!showList || !source) return
    const todo = tracks.filter((tr) => durations[tr.path] === undefined)
    if (todo.length === 0) return

    let cancelled = false
    ;(async () => {
      for (const tr of todo) {
        if (cancelled) return
        try {
          const sec = await loadDurationFor(source, tr.path)
          if (cancelled) return
          setDurations((prev) => ({ ...prev, [tr.path]: sec }))
        } catch {
          if (cancelled) return
          setDurations((prev) => ({ ...prev, [tr.path]: 0 }))
        }
      }
    })()

    return () => { cancelled = true }
  }, [showList, source, tracks, durations])

  if (!visible || !current) return null

  return (
    <div
      role="region"
      aria-label={t('files.audioPlayerLabel')}
      className="shrink-0 border-t border-border bg-card"
    >
      <div className="flex items-center gap-2 sm:gap-3 px-3 sm:px-4 h-[68px]">
        {/* Cover oder Note-Icon */}
        <div className="h-9 w-9 shrink-0 rounded-sm overflow-hidden bg-muted flex items-center justify-center">
          {coverUrl ? (
            <img src={coverUrl} alt="" className="h-full w-full object-cover" />
          ) : (
            <Music2 className="h-4 w-4 text-primary" />
          )}
        </div>

        {/* Titel + Position — schrumpft mit, statt fix w-52 */}
        <div className="min-w-0 flex-1 sm:flex-none sm:w-40 md:w-52 sm:shrink-0">
          <div className="truncate text-sm font-medium" title={current.name}>
            {current.name}
          </div>
          <div className="text-xs text-muted-foreground tabular-nums">
            {index + 1} / {tracks.length}
          </div>
        </div>

        {/* Steuerung */}
        <div className="flex items-center gap-1 shrink-0">
          <button
            type="button"
            aria-label={t('files.audioPlayerPrev')}
            onClick={prev}
            disabled={index === 0}
            className="hidden xs:inline-flex sm:inline-flex rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-30 disabled:pointer-events-none"
          >
            <ChevronLeft className="h-5 w-5" />
          </button>
          <button
            type="button"
            aria-label={playing ? t('files.audioPlayerPause') : t('files.audioPlayerPlay')}
            onClick={togglePlay}
            disabled={!src}
            className="rounded-full p-2 bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {playing ? <Pause className="h-4 w-4" /> : <Play className="h-4 w-4" />}
          </button>
          <button
            type="button"
            aria-label={t('files.audioPlayerNext')}
            onClick={next}
            disabled={index >= tracks.length - 1}
            className="hidden xs:inline-flex sm:inline-flex rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-30 disabled:pointer-events-none"
          >
            <ChevronRight className="h-5 w-5" />
          </button>
        </div>

        {/* Progress — auf engen Screens ausblenden */}
        <div className="hidden md:flex flex-1 items-center gap-2 min-w-0">
          <span className="text-xs text-muted-foreground tabular-nums w-10 text-right">
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
          <span className="text-xs text-muted-foreground tabular-nums w-10">
            {fmt(durationSec)}
          </span>
        </div>

        {/* Volume — Button mutet/entmutet; Hover-Bridge zum Popover. */}
        <div className="relative group hidden sm:block shrink-0">
          <button
            type="button"
            aria-label={volume === 0 ? t('files.audioPlayerUnmute') : t('files.audioPlayerMute')}
            onClick={toggleMute}
            className="rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent"
          >
            {volume === 0 ? <VolumeX className="h-4 w-4" /> : <Volume2 className="h-4 w-4" />}
          </button>
          {/* Popover liegt direkt am Button-Top an + Padding-Bottom als Hover-Brücke.
              Kein Pixel-Gap -> :hover bricht beim Mauswechsel nicht ab. */}
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

        {/* Playlist — ab sm */}
        <button
          type="button"
          aria-label={t('files.audioPlayerToggleList')}
          aria-expanded={showList}
          onClick={() => setShowList((v) => !v)}
          className="hidden sm:inline-flex rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent"
        >
          <ListMusic className="h-5 w-5" />
        </button>
        <button
          type="button"
          onClick={close}
          aria-label={t('files.audioPlayerClose')}
          className="rounded-md p-1.5 text-muted-foreground hover:text-foreground hover:bg-accent"
        >
          <X className="h-5 w-5" />
        </button>
      </div>

      {/* Status / Fehler */}
      {(error || !src) && (
        <div className="px-4 pb-2">
          {error ? (
            <div className="flex items-center gap-2 text-xs text-destructive">
              <AlertCircle className="h-3.5 w-3.5" />
              <span>{error}</span>
            </div>
          ) : (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              <span>{t('files.audioPlayerLoading')}</span>
            </div>
          )}
        </div>
      )}

      {/* Audio-Element ohne Controls */}
      {src && (
        <audio
          ref={audioRef}
          key={current.path}
          src={src}
          aria-label={current.name}
          onTimeUpdate={(e) => {
            const t = e.currentTarget.currentTime
            setCurrentSec(t)
            trackPlayback(t, !e.currentTarget.paused)
          }}
          onLoadedMetadata={(e) => setDurationSec(e.currentTarget.duration || 0)}
          onPlay={() => { setPlaying(true); trackPlayback(audioRef.current?.currentTime ?? 0, true) }}
          onPause={() => { setPlaying(false); trackPlayback(audioRef.current?.currentTime ?? 0, false) }}
          onEnded={next}
          className="hidden"
        />
      )}

      {/* Playlist-Drawer */}
      {showList && (
        <div
          role="list"
          aria-label={t('files.audioPlayerListLabel')}
          className="border-t border-border max-h-64 overflow-y-auto"
        >
          {tracks.map((tr, i) => (
            <button
              key={tr.path}
              role="listitem"
              type="button"
              onClick={() => setIdx(i)}
              aria-current={i === index ? 'true' : undefined}
              className={
                'flex w-full items-center gap-2 px-4 py-1.5 text-left text-sm hover:bg-accent ' +
                (i === index ? 'bg-accent/50 font-medium' : 'text-muted-foreground')
              }
            >
              <Music2 className="h-3.5 w-3.5 shrink-0 opacity-70" />
              <span className="truncate flex-1">{tr.name}</span>
              <span className="text-xs tabular-nums opacity-60 w-12 text-right">
                {durations[tr.path] === undefined ? '—' : fmt(durations[tr.path])}
              </span>
              <span className="text-xs tabular-nums opacity-60 w-6 text-right">{i + 1}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function fmt(sec: number): string {
  if (!isFinite(sec) || sec <= 0) return '0:00'
  const m = Math.floor(sec / 60)
  const s = Math.floor(sec % 60)
  return `${m}:${s.toString().padStart(2, '0')}`
}

// Lädt die Datei und gibt nach loadedmetadata die Duration zurück.
// Browser stoppt die Übertragung intern, sobald wir den Blob nicht weiter
// brauchen — die Datei wird trotzdem heruntergeladen, weil unser Endpoint
// kein Range-Streaming bietet. Akzeptable Kosten gegen Anzeigen der Dauer.
// Läuft über dieselbe Grant-URL wie die Wiedergabe (ADR-0067) — der Browser
// holt für preload="metadata" nur den nötigen Range-Ausschnitt, kein
// Voll-Download mehr nötig, nur um an die Dauer zu kommen.
function loadDurationFor(source: FileSource, path: string): Promise<number> {
  return new Promise((resolve, reject) => {
    mediaStreamUrlFrom(source, path).then(({ url }) => {
      const a   = document.createElement('audio')
      a.preload = 'metadata'
      a.onloadedmetadata = () => {
        const d = a.duration
        if (isFinite(d) && d > 0) resolve(d)
        else reject(new Error('no duration'))
      }
      a.onerror = () => reject(new Error('decode'))
      a.src = url
    }).catch(reject)
  })
}
