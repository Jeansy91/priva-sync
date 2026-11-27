import { lazy, Suspense } from 'react'
import { PreviewDialog } from './PreviewDialog'
import { ModelViewerDialog } from './ModelViewerDialog'
import { BlobViewerDialog } from './BlobViewerDialog'
import { FontViewerDialog } from './FontViewerDialog'
import { OfficeViewerDialog } from './OfficeViewerDialog'
import { ArchiveViewerDialog } from './ArchiveViewerDialog'
import { ImageGalleryDialog } from './ImageGalleryDialog'
import { EpubViewer } from './EpubViewer'
import { ComicViewer } from './ComicViewer'
import { type FilePreviewState, type FilePreviewActions } from './useFilePreviews'

// Monaco is a heavy bundle; keep it out of the initial chunk and load it only
// when a text file is actually opened for editing.
const TextEditorDialog = lazy(() =>
  import('./TextEditorDialog').then((m) => ({ default: m.TextEditorDialog })),
)

// FilePreviewLayer rendert alle Vorschau-Dialoge gestapelt — gesteuert vom
// useFilePreviews-Hook. Die Quelle (Sync-Root oder Mount) kommt aus dem
// State, sodass Datei-Mounts dieselben Viewer wie Verzeichnis-Dateien nutzen.
export function FilePreviewLayer({
  state, actions, onEditorShare,
}: {
  state:   FilePreviewState
  actions: FilePreviewActions
  // Share action for the open editor file. Omitted for mounts (recipients
  // cannot re-share, ADR-0012), which hides the editor's share button.
  onEditorShare?: () => void
}) {
  const { source } = state
  // Ohne Quelle ist nichts offen — alle Targets werden gemeinsam mit source gesetzt.
  if (!source) return null

  return (
    <>
      {state.preview && (
        <PreviewDialog
          source={source}
          path={state.preview.path}
          name={state.preview.name}
          onClose={actions.closePreview}
        />
      )}

      {state.gallery && (
        <ImageGalleryDialog
          source={source}
          images={state.gallery.images}
          startIndex={state.gallery.index}
          onClose={actions.closeGallery}
        />
      )}

      {state.model && (
        <ModelViewerDialog
          source={source}
          path={state.model.path}
          name={state.model.name}
          onClose={actions.closeModel}
        />
      )}

      {state.font && (
        <FontViewerDialog
          source={source}
          path={state.font.path}
          name={state.font.name}
          onClose={actions.closeFont}
        />
      )}

      {state.epub && (
        <BlobViewerDialog
          source={source}
          path={state.epub.path}
          name={state.epub.name}
          onClose={actions.closeEpub}
          render={(b, n) => <EpubViewer blob={b} name={n} />}
        />
      )}

      {state.archive && (
        <ArchiveViewerDialog
          source={source}
          path={state.archive.path}
          name={state.archive.name}
          onClose={actions.closeArchive}
        />
      )}

      {state.comic && (
        <BlobViewerDialog
          source={source}
          path={state.comic.path}
          name={state.comic.name}
          onClose={actions.closeComic}
          render={(b, n) => <ComicViewer blob={b} name={n} />}
        />
      )}

      {state.office && (
        <OfficeViewerDialog
          source={source}
          path={state.office.path}
          name={state.office.name}
          onClose={actions.closeOffice}
        />
      )}

      {state.editor && (
        <Suspense fallback={null}>
          <TextEditorDialog
            source={source}
            path={state.editor.path}
            name={state.editor.name}
            canWrite={state.editor.canWrite}
            onShare={onEditorShare}
            onClose={actions.closeEditor}
          />
        </Suspense>
      )}
    </>
  )
}
