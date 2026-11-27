import { create } from 'zustand'
import type { FileSource } from '../pages/files/fileSource'

export type AudioTrack = { path: string; name: string }

type State = {
  source:  FileSource | null
  tracks:  AudioTrack[]
  index:   number
  visible: boolean
}

type Actions = {
  play:     (source: FileSource, tracks: AudioTrack[], index: number) => void
  setIndex: (i: number) => void
  prev:     () => void
  next:     () => void
  close:    () => void
}

const initial: State = { source: null, tracks: [], index: 0, visible: false }

export const useAudioPlayer = create<State & Actions>((set, get) => ({
  ...initial,
  play: (source, tracks, index) => set({ source, tracks, index, visible: tracks.length > 0 }),
  setIndex: (i) => {
    const { tracks } = get()
    if (i < 0 || i >= tracks.length) return
    set({ index: i })
  },
  prev: () => {
    const { index } = get()
    if (index > 0) set({ index: index - 1 })
  },
  next: () => {
    const { index, tracks } = get()
    if (index < tracks.length - 1) set({ index: index + 1 })
  },
  close: () => set(initial),
}))
