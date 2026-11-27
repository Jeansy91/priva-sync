import { useCallback, useState } from 'react'
import { previewKindFor } from './previewKind'
import { isEditableText } from './editorMode'
import { useAudioPlayer, type AudioTrack } from '../../store/audioPlayer'
import { type DirEntry } from './helpers'
import { type GalleryImage } from './ImageGalleryDialog'
import { type FileSource } from './fileSource'

// Vorschau-Targets pro Kategorie. null = nicht offen.
type Target = { path: string; name: string } | null

// The editor target also carries write permission — a read-only mount opens
// the editor in view-only mode rather than offering Save.
type EditorTarget = { path: string; name: string; canWrite: boolean } | null

export type FilePreviewState = {
  // Quelle der aktuell offenen Vorschau (Sync-Root oder Mount). null = nichts offen.
  source:   FileSource | null
  preview:  Target
  model:    Target
  font:     Target
  epub:     Target
  comic:    Target
  archive:  Target
  office:   Target
  editor:   EditorTarget
  gallery:  { images: GalleryImage[]; index: number } | null
}

export type FilePreviewActions = {
  open:        (entry: DirEntry, fileEntries: DirEntry[], source: FileSource, canWrite?: boolean) => void
  closePreview: () => void
  closeModel:   () => void
  closeFont:    () => void
  closeEpub:    () => void
  closeComic:   () => void
  closeArchive: () => void
  closeOffice:  () => void
  closeEditor:  () => void
  closeGallery: () => void
}

// useFilePreviews kapselt alle Dialog-States für die verschiedenen Vorschau-
// Typen aus FilesPage. Audio wird über den globalen Zustand-Store geöffnet
// (überlebt Route-Wechsel), alles andere ist lokal an FilesPage gebunden.
export function useFilePreviews(): FilePreviewState & FilePreviewActions {
  const [preview, setPreview] = useState<Target>(null)
  const [model,   setModel]   = useState<Target>(null)
  const [font,    setFont]    = useState<Target>(null)
  const [epub,    setEpub]    = useState<Target>(null)
  const [comic,   setComic]   = useState<Target>(null)
  const [archive, setArchive] = useState<Target>(null)
  const [office,  setOffice]  = useState<Target>(null)
  const [editor,  setEditor]  = useState<EditorTarget>(null)
  const [gallery, setGallery] = useState<FilePreviewState['gallery']>(null)
  const [source,  setSource]  = useState<FileSource | null>(null)
  const playAudio = useAudioPlayer((s) => s.play)

  const open = useCallback((entry: DirEntry, fileEntries: DirEntry[], src: FileSource, canWrite = true) => {
    if (entry.kind !== 'file') return
    const kind = previewKindFor(entry.name)
    if (kind === null) {
      // No dedicated viewer, but editable text → open the in-browser editor.
      if (isEditableText(entry.name)) {
        setSource(src)
        setEditor({ path: entry.file.path, name: entry.name, canWrite })
      }
      return
    }
    setSource(src)

    if (kind === 'image' || kind === 'video') {
      const media: GalleryImage[] = fileEntries
        .filter((e) => {
          const k = previewKindFor(e.name)
          return e.kind === 'file' && (k === 'image' || k === 'video')
        })
        .map((e) => ({ path: (e as DirEntry & { kind: 'file' }).file.path, name: e.name }))
      const idx = Math.max(0, media.findIndex((g) => g.path === entry.file.path))
      setGallery({ images: media, index: idx })
      return
    }

    if (kind === 'audio') {
      const tracks: AudioTrack[] = fileEntries
        .filter((e) => e.kind === 'file' && previewKindFor(e.name) === 'audio')
        .map((e) => ({ path: (e as DirEntry & { kind: 'file' }).file.path, name: e.name }))
      const idx = Math.max(0, tracks.findIndex((g) => g.path === entry.file.path))
      playAudio(src, tracks, idx)
      return
    }

    const target = { path: entry.file.path, name: entry.name }
    switch (kind) {
      case 'model3d': setModel(target);   return
      case 'font':    setFont(target);    return
      case 'epub':    setEpub(target);    return
      case 'comic':   setComic(target);   return
      case 'archive': setArchive(target); return
      case 'office':  setOffice(target);  return
      default:        setPreview(target)
    }
  }, [playAudio])

  return {
    source, preview, model, font, epub, comic, archive, office, editor, gallery,
    open,
    closePreview: () => setPreview(null),
    closeModel:   () => setModel(null),
    closeFont:    () => setFont(null),
    closeEpub:    () => setEpub(null),
    closeComic:   () => setComic(null),
    closeArchive: () => setArchive(null),
    closeOffice:  () => setOffice(null),
    closeEditor:  () => setEditor(null),
    closeGallery: () => setGallery(null),
  }
}
