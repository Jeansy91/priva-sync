import type { ReactNode } from 'react'
import { Copy, Download, Eye, FilePen, FolderOpen, FolderPlus, Laptop, LogOut, Move, Pencil, Play, Plus, Share2, Trash2, Upload } from 'lucide-react'
import { createElement } from 'react'
import type { DropdownMenuItem } from '@privasync/ui'
import type { DirEntry } from './helpers'
import { previewKindFor, type PreviewKind } from './previewKind'
import { isEditableText } from './editorMode'

export type EntryMenuCallbacks = {
  onPreview:   (entry: DirEntry) => void
  // Optional: only rendered when supplied. share/download moved to
  // optional too so mount-only context menus can omit them entirely.
  onDownload?: (entry: DirEntry) => void
  onShare?:    (entry: DirEntry) => void
  onDelete?:   (entry: DirEntry) => void  // omitted on empty-area (we are
                                          // already inside that directory)
  onMove?:     (entry: DirEntry) => void
  onCopy?:     (entry: DirEntry) => void  // files + folders; server resolves
                                          // which and books the copied bytes
  onRename?:   (entry: DirEntry) => void  // files + folders; sync-root rename
                                          // has its own action (separate menu)
  onEditText?: (entry: DirEntry) => void  // editable text files only; opens
                                          // the in-browser editor
  onOpen?:     (entry: DirEntry) => void  // dirs only
  onRevealLocal?: (entry: DirEntry) => void // files + dirs; privasync:// deep
                                            // link showing the entry in the OS
                                            // file manager
  onNewFolder?: (entry: DirEntry) => void // dirs only
  onUpload?:   (entry: DirEntry) => void  // dirs only
  // Recipient-side "leave" for Shared Mounts. Rendered as a destructive
  // item near the bottom of the menu. See ADR-0010.
  onLeave?:    (entry: DirEntry) => void
  // Empty-area trigger at /files: no entry exists, only this single item.
  onNewSyncRoot?: () => void
}

const PREVIEW_LABEL: Record<PreviewKind, { key: string; icon: ReactNode }> = {
  image:   { key: 'files.menuPreviewImage', icon: createElement(Eye,  { className: 'h-3.5 w-3.5' }) },
  pdf:     { key: 'files.menuPreviewPdf',   icon: createElement(Eye,  { className: 'h-3.5 w-3.5' }) },
  video:   { key: 'files.menuPreviewVideo', icon: createElement(Play, { className: 'h-3.5 w-3.5' }) },
  audio:   { key: 'files.menuPreviewAudio', icon: createElement(Play, { className: 'h-3.5 w-3.5' }) },
  model3d: { key: 'files.menuPreviewModel', icon: createElement(Eye,  { className: 'h-3.5 w-3.5' }) },
  font:    { key: 'files.menuPreviewFont',  icon: createElement(Eye,  { className: 'h-3.5 w-3.5' }) },
  epub:    { key: 'files.menuPreviewEpub',  icon: createElement(Eye,  { className: 'h-3.5 w-3.5' }) },
  archive: { key: 'files.menuPreviewArchive', icon: createElement(Eye, { className: 'h-3.5 w-3.5' }) },
  comic:   { key: 'files.menuPreviewComic', icon: createElement(Eye,  { className: 'h-3.5 w-3.5' }) },
  office:  { key: 'files.menuPreviewOffice', icon: createElement(Eye, { className: 'h-3.5 w-3.5' }) },
}

// Shared menu spec for both the `...`-Dropdown (EntryMenu) and the right-click
// context menu. Multi-entry support is anticipated but the current callsites
// only ever pass one entry — keep the single-entry shape until the
// bulk-action callbacks exist.
export function buildEntryMenuItems(
  entries: DirEntry[],
  cb:      EntryMenuCallbacks,
  t:       (key: string) => string,
): DropdownMenuItem[] {
  if (entries.length === 0) {
    if (!cb.onNewSyncRoot) return []
    return [{
      label:    t('files.menuNewSyncRoot'),
      icon:     createElement(Plus, { className: 'h-3.5 w-3.5' }),
      onSelect: cb.onNewSyncRoot,
    }]
  }
  // Multi-entry support arrives with the bulk callbacks; fall back to the
  // first entry so the menu still works if multi-mode triggers prematurely.
  const entry = entries[0]
  const items: DropdownMenuItem[] = []

  // "Open" only makes sense for navigable targets (dirs/sync-roots). Files
  // already expose their open-actions via "Vorschau"/"Abspielen" etc.
  if (cb.onOpen && entry.kind === 'dir') {
    items.push({
      label:    t('files.menuOpen'),
      icon:     createElement(FolderOpen, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onOpen!(entry),
    })
  }

  if (entry.kind === 'file') {
    const kind = previewKindFor(entry.name)
    if (kind) {
      const meta = PREVIEW_LABEL[kind]
      items.push({
        label:    t(meta.key),
        icon:     meta.icon,
        onSelect: () => cb.onPreview(entry),
      })
    }
    if (cb.onEditText && isEditableText(entry.name)) {
      items.push({
        label:    t('files.menuEditText'),
        icon:     createElement(FilePen, { className: 'h-3.5 w-3.5' }),
        onSelect: () => cb.onEditText!(entry),
      })
    }
    if (cb.onDownload) {
      items.push({
        label:    t('files.menuDownload'),
        icon:     createElement(Download, { className: 'h-3.5 w-3.5' }),
        onSelect: () => cb.onDownload!(entry),
      })
    }
  } else {
    if (cb.onDownload) {
      items.push({
        label:    t('files.menuDownloadZip'),
        icon:     createElement(Download, { className: 'h-3.5 w-3.5' }),
        onSelect: () => cb.onDownload!(entry),
      })
    }
    if (cb.onNewFolder) {
      items.push({
        label:    t('files.menuNewFolder'),
        icon:     createElement(FolderPlus, { className: 'h-3.5 w-3.5' }),
        onSelect: () => cb.onNewFolder!(entry),
      })
    }
    if (cb.onUpload) {
      items.push({
        label:    t('files.menuUpload'),
        icon:     createElement(Upload, { className: 'h-3.5 w-3.5' }),
        onSelect: () => cb.onUpload!(entry),
      })
    }
  }

  // Show in the local sync folder via privasync:// deep link. Valid for
  // files and folders alike — requires an installed desktop client.
  if (cb.onRevealLocal) {
    items.push({
      label:    t('files.menuRevealLocal'),
      icon:     createElement(Laptop, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onRevealLocal!(entry),
    })
  }

  // ADR-0012: Mounts kann der Empfaenger nicht weiterteilen und
  // nicht loeschen — beide Items werden bei Mount-Entries ausgeblendet,
  // unabhaengig davon, ob der Caller die Callbacks gesetzt hat. Stattdessen
  // gibt es Verlassen.
  const isMount = entry.mount !== undefined

  if (cb.onShare && !isMount) {
    items.push({
      label:    t('files.menuShare'),
      icon:     createElement(Share2, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onShare!(entry),
    })
  }
  if (cb.onMove) {
    items.push({
      label:    t('files.bulkMove'),
      icon:     createElement(Move, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onMove!(entry),
    })
  }
  // Copy works for files and folders. Mounts are excluded — the recipient
  // copies within their own roots, not into a giver's storage.
  if (cb.onCopy && !isMount) {
    items.push({
      label:    t('files.menuCopy'),
      icon:     createElement(Copy, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onCopy!(entry),
    })
  }
  // Rename works for files and folders (the server rewrites the whole subtree
  // for a directory). Mounts are excluded — the recipient cannot rename them.
  if (cb.onRename && !isMount) {
    items.push({
      label:    t('files.menuRename'),
      icon:     createElement(Pencil, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onRename!(entry),
    })
  }
  if (cb.onLeave && isMount) {
    items.push({
      label:    t('files.mountLeave'),
      icon:     createElement(LogOut, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onLeave!(entry),
      variant:  'destructive',
    })
  }
  if (cb.onDelete && !isMount) {
    items.push({
      label:    t('files.menuDelete'),
      icon:     createElement(Trash2, { className: 'h-3.5 w-3.5' }),
      onSelect: () => cb.onDelete!(entry),
      variant:  'destructive',
    })
  }

  return items
}
