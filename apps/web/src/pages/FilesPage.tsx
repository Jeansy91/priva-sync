import { useState, useEffect, useCallback, useMemo } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import {
  Folder, Upload,
  AlertCircle, Loader2,
} from 'lucide-react'
import { Button, cn } from '@privasync/ui'
import { useAuthStore } from '../store/auth'
import { useResizableSidebar } from '../hooks/useResizableSidebar'
import type { SyncRoot, SharedMount } from '@privasync/api-client'
import { useTranslation } from '@privasync/i18n'
import {
  buildDirSelection, parentPath,
  toFilesUrl, toMountUrl, type DirEntry,
} from './files/helpers'
import { parseFilesUrl } from './files/filesUrl'
import { revealLocally } from './files/revealLocal'
import { UploadQueue } from './files/UploadQueue'
import { FullBreadcrumbs } from './files/FullBreadcrumbs'
import { CreateRootDialog } from './files/CreateRootDialog'
import { CreateFolderDialog } from './files/CreateFolderDialog'
import { useFilePreviews } from './files/useFilePreviews'
import { FilePreviewLayer } from './files/FilePreviewLayer'
import { FileDetailPanel, type DetailSelection } from './files/FileDetailPanel'
import { previewKindFor } from './files/previewKind'
import { isEditableText } from './files/editorMode'
import { FilesTableView } from './files/FilesTableView'
import { FilesGridView } from './files/FilesGridView'
import { FilesSearchResults } from './files/FilesSearchResults'
import { FilesGlobalSearchResults } from './files/FilesGlobalSearchResults'
import { type FileSource } from './files/fileSource'
import { EntryContextMenu } from './files/EntryContextMenu'
import type { EntryMenuCallbacks } from './files/entryMenuItems'
import { entryKey, type FilesViewCallbacks, type MultiselectViewProps } from './files/filesViewTypes'
import { useMultiselect } from './files/useMultiselect'
import { useBulkActions } from './files/useBulkActions'
import { useFilesDnD, isInternalDrag } from './files/useFilesDnD'
import { BulkDeleteDialog } from './files/BulkDeleteDialog'
import { BulkMoveDialog } from './files/BulkMoveDialog'
import { useCreateFolder } from './files/useCreateFolder'
import { useRename } from './files/useRename'
import { useCopy } from './files/useCopy'
import { useSyncRootRename } from './files/useSyncRootRename'
import { RenameDialog } from './files/RenameDialog'
import { CopyDialog } from './files/CopyDialog'
import { useRootShares } from './files/hooks/useRootShares'
import { useMountActions } from './files/hooks/useMountActions'
import {
  TREE_WIDTH_KEY, TREE_WIDTH_MIN, TREE_WIDTH_MAX, TREE_WIDTH_DEFAULT,
  basename, nextMountSelection,
} from './files/filesPagePrefs'
import { useFilesViewPrefs } from './files/useFilesViewPrefs'
import { useFilesPageDialogs } from './files/useFilesPageDialogs'
import { useFilesSearch } from './files/useFilesSearch'
import { useFilesData } from './files/useFilesData'
import { useFilesSSE } from './files/useFilesSSE'
import { useFilesDetail } from './files/useFilesDetail'
import { useFilesEntries } from './files/useFilesEntries'
import { useMountFileStats } from './files/hooks/useMountFileStats'
import { useFilesUpload } from './files/useFilesUpload'
import { useFilesNavigation, type PendingAction } from './files/useFilesNavigation'
import { useFilesLasso } from './files/useFilesLasso'
import { useFilesRootActions } from './files/useFilesRootActions'
import { useFilesEntryActions } from './files/useFilesEntryActions'
import { useFilesKeyboardShortcuts } from './files/useFilesKeyboardShortcuts'
import { DeleteEntryDialog } from './files/DeleteEntryDialog'
import { DeleteRootDialog } from './files/DeleteRootDialog'
import { MountLeaveDialog } from './files/MountLeaveDialog'
import { FilesSearchBar } from './files/FilesSearchBar'
import { FilesActionsBar } from './files/FilesActionsBar'
import { FilesPageTree } from './files/FilesPageTree'
import { useFilesUrlSync } from './files/useFilesUrlSync'
import { FilesNoRootEmptyState } from './files/FilesNoRootEmptyState'

export default function FilesPage() {
  const { t, i18n } = useTranslation()
  const localeTag = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const go     = useNavigate()
  const params = useParams<{ '*': string }>()
  const [urlParams, setUrlParams] = useSearchParams()
  // Default false until the user record has loaded — avoids flashing an empty
  // sidebar strip during the initial auth bootstrap.
  const showFileTree       = useAuthStore((s) => s.user?.show_file_tree ?? false)
  const showFileCheckboxes = useAuthStore((s) => s.user?.show_file_checkboxes ?? true)

  const sidebar = useResizableSidebar({
    storageKey:   TREE_WIDTH_KEY,
    min:          TREE_WIDTH_MIN,
    max:          TREE_WIDTH_MAX,
    defaultWidth: TREE_WIDTH_DEFAULT,
  })

  const { mountMode, mountIDParam, mountSubPath, urlRootName, urlPath } =
    parseFilesUrl(params['*'] ?? '')

  const [selectedRoot, setSelectedRoot] = useState<SyncRoot | null>(null)
  const [currentPath, setCurrentPath]   = useState('/')
  const {
    syncRoots, setSyncRoots,
    mounts, setMounts,
    sharesByPath, setSharesByPath,
    allFiles, setAllFiles,
    loading, setLoading,
    error, setError,
    filesNextCursor,
    loadRoots, loadFiles, loadMoreFiles,
  } = useFilesData({ selectedRoot, currentPath, mountMode, t })
  const [treeReloadToken, setTreeReloadToken] = useState(0)
  const bumpTreeReload = useCallback(() => setTreeReloadToken((n) => n + 1), [])

  const {
    createRootOpen, setCreateRootOpen,
    newRootName, setNewRootName,
    createRootLoading, setCreateRootLoading,
    createRootError, setCreateRootError,
    deleteTarget, setDeleteTarget,
    deleteLoading, setDeleteLoading,
    ctxMenu, setCtxMenu,
    deleteRootTarget, setDeleteRootTarget,
    deleteRootConfirm, setDeleteRootConfirm,
    deleteRootLoading, setDeleteRootLoading,
    downloadingRoot, setDownloadingRoot,
  } = useFilesPageDialogs()
  const [detailSelection, setDetailSelection] = useState<DetailSelection | null>(null)
  // Sync-root list row that the user has highlighted via single-click. Parallel
  // to detailSelection but only used at /files where no sync root is open yet.
  const [selectedSyncRootID, setSelectedSyncRootID] = useState<string | null>(null)
  // "Neuer Ordner"/"Hochladen" from a non-current target: enter the target
  // first, then fire the action once the path has settled.
  const [pendingAction, setPendingAction] = useState<PendingAction | null>(null)
  const urlQuery = urlParams.get('q') ?? ''
  const searchQuery = urlQuery
  const { detailsVisible, setDetailsVisible, viewMode, setViewMode } = useFilesViewPrefs()
  const previews = useFilePreviews()

  const multi = useMultiselect()

  const removeMountFromList = useCallback((mountID: string) => {
    setMounts((prev) => prev.filter((m) => m.id !== mountID))
  }, [])

  const {
    downloadMountFile, downloadMountZip,
    mountLeaveTarget, setMountLeaveTarget, mountLeaveError, setMountLeaveError, mountLeaveLoading,
    handleLeaveMount,
  } = useMountActions(setError, removeMountFromList)

  const { downloadRootSubtree, downloadRoot, handleDeleteRoot, handleCreateRoot } = useFilesRootActions({
    selectedRoot, setSelectedRoot, setSyncRoots, setError, t, go,
    newRootName, setNewRootName, setCreateRootOpen, setCreateRootLoading, setCreateRootError,
    deleteRootTarget, deleteRootConfirm, setDeleteRootTarget, setDeleteRootConfirm, setDeleteRootLoading,
    setDownloadingRoot,
  })

  const {
    uploadBtnRef,
    uploadQueue, setUploadQueue,
    dragOver, setDragOver,
    uploadFiles, handleDrop,
  } = useFilesUpload({ selectedRoot, currentPath, mountMode, t, loadFiles })

  // ─── Sync state from URL whenever roots load or URL changes ──────────────────

  useFilesUrlSync({
    mountMode, mountIDParam, mountSubPath, urlRootName, urlPath,
    syncRoots, mounts, setSelectedRoot, setCurrentPath,
  })

  const { rootSharesByRootID, refreshRootShares } = useRootShares(
    syncRoots, selectedRoot, mountMode, urlRootName,
  )

  useFilesSSE({
    selectedRoot, setSelectedRoot, currentPath, allFiles,
    mountMode, mountIDParam, syncRoots,
    setSyncRoots, setMounts, setSharesByPath,
    bumpTreeReload, refreshRootShares,
    loadFiles, loadRoots, go,
  })

  // Mount the caller is currently browsing, if any. Drives write-button
  // visibility (read-only mounts hide upload/new-folder).
  const activeMount = useMemo(
    () => (mountMode && mountIDParam ? mounts.find((m) => m.id === mountIDParam) ?? null : null),
    [mountMode, mountIDParam, mounts],
  )
  const activeMountCanWrite = !!activeMount?.can_write

  // Lazy file metadata for file-mounts in the root list (date/size). Mirrors
  // useDirStats for sync-root directories. See ADR-0012.
  const mountFileStats = useMountFileStats(mounts)

  const {
    entries, fileEntries, filesSort,
    rootByName, rootSectionsRaw, rootListSharesByPath, rootTableSections,
    displayEntries, orderedKeys,
  } = useFilesEntries({
    selectedRoot, currentPath, allFiles,
    syncRoots, mounts, mountFileStats, rootSharesByRootID, t,
  })

  // Preview source for entries inside the currently open root/mount. Mount-
  // browse uses a SyncRoot facade whose id is the share id (see
  // useFilesUrlSync), so mountMode flips the dispatch to the mount endpoint.
  const inRootSource = useMemo<FileSource | null>(
    () => selectedRoot
      ? (mountMode
          ? { kind: 'mount', mountID: selectedRoot.id }
          : { kind: 'root',  rootID:  selectedRoot.id })
      : null,
    [selectedRoot, mountMode],
  )

  const openPreview = useCallback(
    (entry: DirEntry, source?: FileSource, siblings?: DirEntry[]) => {
      const src = source ?? inRootSource
      if (!src) return
      // Write permission gates the editor's Save: sync-roots are always
      // writable; a mount only when its grant allows it.
      const canWrite = src.kind === 'mount'
        ? !!mounts.find((m) => m.id === src.mountID)?.can_write
        : true
      previews.open(entry, siblings ?? fileEntries, src, canWrite)
    },
    [previews, fileEntries, inRootSource, mounts],
  )

  // Opens the full preview layer for a single-file mount from the root list.
  // The mount root is the file, so the sub-path is "/" and it is its own and
  // only gallery sibling.
  const openMountFilePreview = useCallback((m: SharedMount) => {
    const entry: DirEntry = {
      kind: 'file',
      name: m.name,
      mount: m,
      file: {
        id: 'mount:' + m.id, path: '/', size: 0, mtime: '',
        content_hash: '', is_deleted: false, updated_at: '',
      },
    }
    openPreview(entry, { kind: 'mount', mountID: m.id }, [entry])
  }, [openPreview])

  const searchActive = searchQuery.trim() !== ''
  const {
    searchInput, setSearchInput,
    searchHits,
    searchNextCursor, searchLoading, searchError,
    globalGroups, globalLoading, globalError,
    loadMoreSearch, loadMoreGlobalGroup,
    commitSearch, clearSearch,
  } = useFilesSearch({
    searchActive, selectedRoot, currentPath,
    searchQuery, urlQuery, mounts, t, setUrlParams,
  })

  const globalRootByID = useMemo(
    () => new Map(syncRoots.map((r) => [r.id, r])),
    [syncRoots],
  )

  const { detailRoot, isListLevel, effectiveSelection } = useFilesDetail({
    selectedRoot, currentPath, allFiles, syncRoots, selectedSyncRootID,
    detailSelection, setDetailSelection,
  })

  // ─── Create folder (inside a sync root) ───────────────────────────────────

  const createFolder = useCreateFolder({
    selectedRoot,
    allFiles,
    currentPath,
    refresh: async () => {
      if (selectedRoot) await loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    },
  })

  const rename = useRename({
    selectedRoot,
    allFiles,
    refresh: async () => {
      if (selectedRoot) await loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    },
  })

  const copy = useCopy({
    selectedRoot,
    allFiles,
    refresh: async () => {
      if (selectedRoot) await loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    },
  })

  // ─── Navigation ────────────────────────────────────────────────────────────

  const { navigateToRoot, goShare, navigate, enterSyncRoot, triggerNewFolder, triggerUpload } =
    useFilesNavigation({
      go, selectedRoot, currentPath, mountMode,
      createFolderOpenFor: createFolder.openFor,
      uploadBtnRef, setPendingAction,
    })

  const syncRootRename = useSyncRootRename({
    syncRoots,
    reload: async () => { loadRoots(); bumpTreeReload() },
  })

  // Pending action from a sync-root or sibling-dir context menu: fire once the
  // requested path is actually open. createFolder takes the path directly,
  // upload always targets `currentPath` so we wait for it to settle.
  useEffect(() => {
    if (!pendingAction || !selectedRoot) return
    if (selectedRoot.id !== pendingAction.rootID) return
    if (currentPath !== pendingAction.path) return
    if (pendingAction.kind === 'newFolder') createFolder.openFor(pendingAction.path)
    else uploadBtnRef.current?.openFilePicker()
    setPendingAction(null)
  }, [pendingAction, selectedRoot, currentPath, createFolder])


  const { handleDownload, handleDelete } = useFilesEntryActions({
    selectedRoot, currentPath, mountMode, t,
    loadFiles, bumpTreeReload, setError,
    deleteTarget, setDeleteTarget, setDeleteLoading,
  })

  // ─── Render ────────────────────────────────────────────────────────────────

  const entryCallbacks = (root: SyncRoot): EntryMenuCallbacks => ({
    onDelete:   setDeleteTarget,
    onDownload: handleDownload,
    onPreview:  openPreview,
    onShare:    (e) => goShare(root.id, e.kind === 'file' ? e.file.path : e.path, e.kind !== 'file'),
    onMove:     (e) => {
      // Single-entry move via menu: stage the row as the selection so the
      // bulk-move pipeline applies. handleMove clears it again on success.
      multi.replaceSelection(new Set([entryKey(e)]))
      bulk.move.openDialog()
    },
    onCopy:     copy.openFor,
    onRename:   rename.openFor,
    onEditText: (e) => openPreview(e),
    onRevealLocal: (e) => revealLocally(root.id, e.kind === 'file' ? e.file.path : e.path),
    onOpen:     (e) => {
      if (e.kind === 'dir') navigate(e.path)
      else if (previewKindFor(e.name) !== null || isEditableText(e.name)) openPreview(e)
      else handleDownload(e)
    },
    // onNewFolder + onUpload are added only by the empty-area trigger —
    // creating/uploading belongs to "I'm here now", not to a row that points
    // elsewhere.
  })

  // Sync-root level: empty-area right-click hits actions that target the root
  // itself — delete goes through the confirmation dialog, not the file delete.
  const syncRootCallbacks = (root: SyncRoot): EntryMenuCallbacks => ({
    onDelete:   () => setDeleteRootTarget(root),
    onDownload: () => { void downloadRoot(root) },
    onPreview:  () => {},
    onShare:    () => goShare(root.id, '/', true),
    onRename:   () => syncRootRename.openFor(root),
    onRevealLocal: () => revealLocally(root.id, '/'),
    onOpen:     () => enterSyncRoot(root),
    // onNewFolder + onUpload added only by the empty-area trigger.
  })

  const multiselectProps: MultiselectViewProps = useMemo(() => ({
    selected: multi.selected,
    onRowClick: (entry, e) => {
      // Row body click: drives selection + detail panel. No modifier =
      // single-row select + open detail; with modifier = multi-select only.
      const k = entryKey(entry)
      if (e.shiftKey || e.ctrlKey || e.metaKey) {
        // Multi-add via modifier drops any lingering single-row detail focus
        // so no second highlight competes with the multi-checked rows.
        multi.handleClick(k, e, orderedKeys)
        setDetailSelection(null)
        return
      }
      multi.handleClick(k, e, orderedKeys)
      if (entry.kind === 'file') setDetailSelection({ kind: 'file', file: entry.file })
      else setDetailSelection(buildDirSelection(entry.path, entry.name))
    },
    onCheckboxClick: (entry, e) => {
      // Treat checkbox click as ctrl-click (toggle, no range collapse).
      const k = entryKey(entry)
      multi.handleClick(k, { ...e, ctrlKey: true } as React.MouseEvent, orderedKeys)
    },
    onToggleAll: () => { multi.toggleAll(orderedKeys) },
    allSelected: orderedKeys.length > 0 && orderedKeys.every((k) => multi.selected.has(k)),
    anySelected: multi.selected.size > 0,
    showCheckboxes: showFileCheckboxes,
  }), [multi, orderedKeys, showFileCheckboxes])

  // Reset selection on path / root change.
  useEffect(() => { multi.clear() }, [selectedRoot?.id, currentPath])

  // While a true multi-selection is active (>1), drop the single-row detail
  // focus so no row gets a second highlight on top of the checked-state.
  useEffect(() => {
    if (multi.selected.size > 1 && detailSelection !== null) setDetailSelection(null)
  }, [multi.selected.size, detailSelection])

  const bulk = useBulkActions({
    selectedRoot,
    currentPath,
    multi,
    mountMode,
    onError: setError,
    refresh: async () => {
      if (selectedRoot) await loadFiles(selectedRoot.id, currentPath)
      bumpTreeReload()
    },
  })

  // Drag-and-drop move. Disabled at the root list, during search, and inside
  // read-only mounts — the move would be rejected anyway.
  const dnd = useFilesDnD({
    multi,
    moveTo: bulk.moveTo,
    enabled: !!selectedRoot && !searchActive && (!mountMode || activeMountCanWrite),
  })

  useFilesKeyboardShortcuts({
    selectedRoot, orderedKeys, multi, ctxMenu,
    openBulkDelete: bulk.delete.openDialog,
  })

  const { lasso, handleLassoMouseDown } = useFilesLasso({
    multi, setDetailSelection, setSelectedSyncRootID,
  })

  const bulkContextCallbacks = (root: SyncRoot): EntryMenuCallbacks => ({
    // Bulk variant: the right-clicked entry is one of many. Each action
    // targets the whole multi-selection through the existing bulk pipeline.
    onDownload: () => { void bulk.download() },
    onPreview:  () => {},
    onShare:    (e) => goShare(root.id, e.kind === 'file' ? e.file.path : e.path, e.kind !== 'file'),
    onMove:     () => bulk.move.openDialog(),
    onDelete:   () => bulk.delete.openDialog(),
  })

  const rootViewCallbacks: FilesViewCallbacks = useMemo(() => {
    // Look up the SyncRoot behind a synthetic entry — by Name, weil die
    // Root-Liste DirEntries baut, die name = root.name tragen. Mount-
    // Eintraege duerfen hier nicht mit eigenen Roots verwechselt werden,
    // also via entry.mount-Decoration dispatchen.
    const rootFor = (entry: DirEntry): SyncRoot | null =>
      entry.mount ? null : rootByName.get(entry.name) ?? null
    // Shared entry-callbacks fuer den `...`-Trigger UND fuer den
    // Right-Click via onContextMenu. buildEntryMenuItems filtert
    // Share/Delete bei Mount-Entries automatisch raus und ersetzt sie
    // durch Verlassen (ADR-0012).
    const cb: EntryMenuCallbacks = {
      onPreview: (e) => {
        if (e.kind !== 'file' || !e.mount) return
        if (previewKindFor(e.name) !== null) openMountFilePreview(e.mount)
      },
      onOpen: (e) => {
        if (e.kind !== 'dir') return
        if (e.mount) { go(toMountUrl(e.mount.id)); return }
        const r = rootFor(e); if (r) enterSyncRoot(r)
      },
      onDownload: (e) => {
        if (e.mount) {
          if (e.kind === 'file') void downloadMountFile(e.mount)
          else                   void downloadMountZip(e.mount)
          return
        }
        const r = rootFor(e); if (r) void downloadRoot(r)
      },
      onShare: (e) => {
        const r = rootFor(e)
        if (r) goShare(r.id, '/', true)
      },
      onRevealLocal: (e) => {
        // Mounts sync under their share id (desktop server_root_id), own
        // roots under the server root id — both reveal the root itself.
        if (e.mount) { revealLocally(e.mount.id, '/'); return }
        const r = rootFor(e); if (r) revealLocally(r.id, '/')
      },
      onDelete: (e) => { const r = rootFor(e); if (r) setDeleteRootTarget(r) },
      onRename: (e) => { const r = rootFor(e); if (r) syncRootRename.openFor(r) },
      onLeave: (e) => {
        if (!e.mount) return
        setMountLeaveError(null)
        setMountLeaveTarget(e.mount)
      },
    }
    return {
      onSelect: (sel) => {
        // Root-Liste hat keinen Multi-Select. Mount-Selection traegt
        // die Decoration, eigene Roots werden ueber selectedSyncRootID
        // hervorgehoben.
        if (sel.mount) {
          const m = sel.mount
          setSelectedSyncRootID(m.id)
          setDetailSelection((prev) => nextMountSelection(prev, m))
          return
        }
        if (sel.kind !== 'dir') return
        const r = rootByName.get(sel.name)
        setSelectedSyncRootID(r?.id ?? null)
        setDetailSelection(null)
      },
      onActivateDir: (path) => {
        // Dispatch ueber den path-Lookup — der Row liefert nur den path,
        // die Entry-Info holen wir aus der aktuellen Sektion.
        for (const s of rootSectionsRaw) {
          for (const e of s.entries) {
            if (e.kind === 'dir' && e.path === path) {
              if (e.mount) { go(toMountUrl(e.mount.id)); return }
              const r = rootByName.get(e.name); if (r) enterSyncRoot(r)
              return
            }
          }
        }
      },
      onActivateFile: (entry) => {
        // Datei-Mounts: Vorschau wenn moeglich, sonst Direkt-Download.
        if (entry.kind === 'file' && entry.mount) {
          if (previewKindFor(entry.name) !== null || isEditableText(entry.name)) openMountFilePreview(entry.mount)
          else                                                                    void downloadMountFile(entry.mount)
        }
      },
      entryCallbacks: cb,
      onContextMenu:  (entry, x, y) => setCtxMenu({ entry, anchor: { x, y }, callbacks: cb }),
      // Kein Multi-Select auf Root-Ebene — Sync-Ordner werden einzeln geoeffnet.
      multiselect:    undefined,
    }
  }, [rootByName, downloadRoot, enterSyncRoot, openMountFilePreview, syncRootRename])

  const makeViewCallbacks = (root: SyncRoot): FilesViewCallbacks => {
    const cb = entryCallbacks(root)
    return {
      onSelect:       setDetailSelection,
      onActivateDir:  navigate,
      onActivateFile: (e) => (previewKindFor(e.name) !== null || isEditableText(e.name)) ? openPreview(e) : handleDownload(e),
      entryCallbacks: cb,
      onContextMenu:  (entry, x, y) => {
        const k = entryKey(entry)
        const inMulti = multi.selected.has(k) && multi.selected.size > 1
        if (!inMulti) {
          // Right-click on a non-selected entry replaces the selection with
          // just that row — same as in the Windows Explorer.
          multi.replaceSelection(new Set([k]))
        }
        const callbacks = inMulti ? bulkContextCallbacks(root) : cb
        setCtxMenu({ entry, anchor: { x, y }, callbacks })
      },
      multiselect:    multiselectProps,
      dnd,
    }
  }

  return (
    <div
      className={cn('flex flex-col h-full', dragOver && 'ring-2 ring-primary ring-inset')}
      onDragOver={(e) => {
        // Internal move drags are handled by the per-folder/crumb/tree drop
        // zones — skip the OS-file upload overlay for them.
        if (isInternalDrag(e)) return
        e.preventDefault(); if (selectedRoot) setDragOver(true)
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => { if (isInternalDrag(e)) return; void handleDrop(e) }}
    >
      {/* ─── Toolbar Row 1: Breadcrumbs + Suche ──────────────────── */}
      <div className="flex h-11 items-center gap-2 border-b border-border bg-background px-4 shrink-0">
        <div className="flex-1 min-w-0">
          <FullBreadcrumbs
            root={selectedRoot}
            path={currentPath}
            onNavigateRoot={navigateToRoot}
            onNavigatePath={navigate}
            dnd={dnd}
          />
        </div>
        <FilesSearchBar
          selectedRoot={selectedRoot}
          value={searchInput}
          setValue={setSearchInput}
          onCommit={commitSearch}
          onClear={clearSearch}
        />
      </div>

      <FilesActionsBar
        selectedRoot={selectedRoot}
        mountMode={mountMode}
        activeMountCanWrite={activeMountCanWrite}
        onNewFolder={createFolder.openHere}
        onCreateRoot={() => setCreateRootOpen(true)}
        uploadBtnRef={uploadBtnRef}
        onUploadFiles={uploadFiles}
        filesSort={filesSort}
        viewMode={viewMode}
        setViewMode={setViewMode}
        detailsVisible={detailsVisible}
        setDetailsVisible={setDetailsVisible}
      />

      {/* ─── Body: Sidebar + main area ──────────────────────────── */}
      <div className="flex flex-1 min-h-0">
        {showFileTree && !searchActive && (
          <FilesPageTree
            syncRoots={syncRoots}
            mounts={mounts}
            activeRootID={selectedRoot?.id ?? null}
            activePath={currentPath}
            reloadToken={treeReloadToken}
            go={go}
            width={sidebar.width}
            min={sidebar.min}
            max={sidebar.max}
            startResize={sidebar.startResize}
            onKeyDown={sidebar.onKeyDown}
            dnd={dnd}
          />
        )}
        <div className="flex flex-col flex-1 min-w-0">

      {error && (
        <div className="mx-4 mt-3 flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {error}
        </div>
      )}

      <div className="flex flex-1 min-h-0">
      <div
        className="flex-1 overflow-auto relative"
        data-files-content
        onMouseDown={(e) => {
          if (searchActive) return
          if (selectedRoot) { handleLassoMouseDown(e); return }
          // Root level: no lasso, but free-area mousedown still drops the
          // highlighted sync-root so the detail panel deselects in sync.
          if (!(e.target instanceof Element)) return
          if (e.target.closest('[data-entry-key]')) return
          setSelectedSyncRootID(null)
          setDetailSelection(null)
        }}
        onContextMenu={(e) => {
          // Empty-area right-click. Three flavours:
          //   /files                 -> "Neuer Sync-Ordner"
          //   inside root, path /    -> sync-root actions + new folder
          //   inside root, sub-path  -> directory actions + new folder
          if (searchActive) return
          if (!selectedRoot) {
            e.preventDefault()
            setSelectedSyncRootID(null)
            setDetailSelection(null)
            setCtxMenu({
              entry:     null,
              anchor:    { x: e.clientX, y: e.clientY },
              callbacks: {
                onDelete: () => {}, onDownload: () => {}, onPreview: () => {}, onShare: () => {},
                onNewSyncRoot: () => setCreateRootOpen(true),
              },
            })
            return
          }
          e.preventDefault()
          // Right-click on empty surface drops the multi-selection and the
          // single-row detail focus, mirroring the row case where right-click
          // replaces selection with that row.
          if (multi.selected.size > 0) multi.clear()
          setDetailSelection(null)
          setSelectedSyncRootID(null)
          const atRoot = currentPath === '/' || currentPath === ''
          const entry: DirEntry = atRoot
            ? { kind: 'dir', name: selectedRoot.name, path: '/' }
            : { kind: 'dir', name: basename(currentPath), path: currentPath }
          const base = atRoot ? syncRootCallbacks(selectedRoot) : entryCallbacks(selectedRoot)
          setCtxMenu({
            entry,
            anchor:    { x: e.clientX, y: e.clientY },
            // Empty-area: drop Open + Delete (we are already inside that
            // directory), add NewFolder + Upload that target it.
            callbacks: {
              onDownload:  base.onDownload,
              onPreview:   base.onPreview,
              onShare:     base.onShare,
              onNewFolder: () => triggerNewFolder(selectedRoot, atRoot ? '/' : currentPath),
              onUpload:    () => triggerUpload(selectedRoot,    atRoot ? '/' : currentPath),
            },
          })
        }}
      >
        {loading ? (
          <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
            <Loader2 className="h-5 w-5 animate-spin" />
            <span className="text-sm">{t('files.loading')}</span>
          </div>
        ) : !selectedRoot && searchActive ? (
          <FilesGlobalSearchResults
            groups={globalGroups}
            loading={globalLoading}
            error={globalError}
            query={searchQuery}
            localeTag={localeTag}
            rootByID={globalRootByID}
            onActivateFile={(root, f) => {
              go(toFilesUrl(root.name, parentPath(f.path)))
            }}
            onActivateDir={(root, path) => {
              go(toFilesUrl(root.name, path))
            }}
            onActivateMountFile={(mountID, f) => {
              go(toMountUrl(mountID, parentPath(f.path)))
            }}
            onActivateMountDir={(mountID, path) => {
              go(toMountUrl(mountID, path))
            }}
            onLoadMore={loadMoreGlobalGroup}
          />
        ) : !selectedRoot ? (
          syncRoots.length === 0 && mounts.length === 0 ? (
            <FilesNoRootEmptyState onCreateRoot={() => setCreateRootOpen(true)} />
          ) : (
            viewMode === 'grid' ? (
              <FilesGridView
                sections={rootTableSections}
                selection={detailSelection}
                sharesByPath={rootListSharesByPath}
                {...rootViewCallbacks}
              />
            ) : (
              <FilesTableView
                sections={rootTableSections}
                selection={detailSelection}
                localeTag={localeTag}
                sort={filesSort}
                sharesByPath={rootListSharesByPath}
                {...rootViewCallbacks}
              />
            )
          )
        ) : searchActive ? (
          <FilesSearchResults
            hits={searchHits}
            selection={detailSelection}
            selectedRoot={selectedRoot}
            localeTag={localeTag}
            query={searchQuery}
            loading={searchLoading}
            error={searchError}
            nextCursor={searchNextCursor}
            onLoadMore={loadMoreSearch}
            {...makeViewCallbacks(selectedRoot)}
          />
        ) : entries.length === 0 ? (
          <div
            className="flex flex-col items-center justify-center py-20 gap-3 text-center cursor-pointer"
            onClick={() => uploadBtnRef.current?.openFilePicker()}
          >
            <Upload className="h-10 w-10 text-muted-foreground" />
            <div>
              <p className="text-sm font-medium">{t('files.emptyDirTitle')}</p>
              <p className="text-xs text-muted-foreground mt-1">{t('files.emptyDirHint')}</p>
            </div>
          </div>
        ) : viewMode === 'grid' ? (
          <FilesGridView
            entries={displayEntries}
            selection={detailSelection}
            selectedRoot={selectedRoot}
            mountMode={mountMode}
            sharesByPath={sharesByPath}
            {...makeViewCallbacks(selectedRoot)}
          />
        ) : (
          <FilesTableView
            entries={displayEntries}
            selection={detailSelection}
            selectedRoot={selectedRoot}
            mountMode={mountMode}
            localeTag={localeTag}
            sort={filesSort}
            sharesByPath={sharesByPath}
            {...makeViewCallbacks(selectedRoot)}
          />
        )}
        {lasso.rect && (
          <div
            data-testid="lasso-overlay"
            style={{
              position: 'absolute',
              left:   lasso.rect.x,
              top:    lasso.rect.y,
              width:  lasso.rect.w,
              height: lasso.rect.h,
              pointerEvents: 'none',
            }}
            className="border border-primary bg-primary/10 z-20"
          />
        )}
        {filesNextCursor && !searchActive && (
          <div className="flex justify-center py-4">
            <Button size="sm" variant="outline" onClick={loadMoreFiles} disabled={loading}>
              {loading
                ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                : t('files.searchLoadMore')}
            </Button>
          </div>
        )}
      </div>
      {(detailRoot || effectiveSelection?.mount) && (detailsVisible || multi.selected.size > 0) && effectiveSelection && (
        <div className="hidden lg:flex w-80 shrink-0 overflow-hidden">
          <FileDetailPanel
            rootID={detailRoot?.id}
            rootName={detailRoot?.name}
            mountMode={mountMode}
            owner={detailRoot?.owner ?? null}
            selection={effectiveSelection}
            multiCount={multi.selected.size}
            bulk={{
              onDelete:   bulk.delete.openDialog,
              onMove:     bulk.move.openDialog,
              onDownload: bulk.download,
              onClear:    () => multi.clear(),
            }}
            onShare={() => {
              // Teilen is gated to non-mount entries in the panel itself.
              if (!detailRoot || effectiveSelection.mount) return
              goShare(
                detailRoot.id,
                effectiveSelection.kind === 'file' ? effectiveSelection.file.path : effectiveSelection.path,
                effectiveSelection.kind !== 'file',
              )
            }}
            onDownload={() => {
              // Mount dispatch (ADR-0012): file-mount → single-file
              // stream, dir-mount → ZIP via token. Eigene Datei/Dir
              // routes through handleDownload like before.
              if (effectiveSelection.mount) {
                const m = effectiveSelection.mount
                if (m.is_file) void downloadMountFile(m)
                else void downloadMountZip(m)
                return
              }
              if (effectiveSelection.kind === 'file') {
                const f = effectiveSelection.file
                handleDownload({ kind: 'file', name: basename(f.path), file: f })
                return
              }
              // Eigener Sync-Root / Subdir → ZIP des Subtrees.
              if (!detailRoot) return
              void downloadRootSubtree(detailRoot, effectiveSelection.path)
            }}
            onPreview={() => {
              if (effectiveSelection.kind !== 'file') return
              // File-mount from the root list: preview via the mount endpoint.
              if (effectiveSelection.mount?.is_file) {
                openMountFilePreview(effectiveSelection.mount)
                return
              }
              const f = effectiveSelection.file
              openPreview({ kind: 'file', name: basename(f.path), file: f })
            }}
          />
        </div>
      )}
      </div>

      <UploadQueue items={uploadQueue} onClose={() => setUploadQueue([])} />

      {dragOver && (
        <div className="absolute inset-0 flex items-center justify-center bg-background/80 pointer-events-none z-30">
          <div className="rounded-xl border-2 border-dashed border-primary px-10 py-8 text-center">
            <Upload className="h-8 w-8 mx-auto text-primary mb-2" />
            <p className="text-sm font-medium">{t('files.dropOverlay')}</p>
          </div>
        </div>
      )}

      <CreateRootDialog
        open={createRootOpen}
        name={newRootName}
        loading={createRootLoading}
        error={createRootError}
        onNameChange={setNewRootName}
        onConfirm={handleCreateRoot}
        onClose={() => { setCreateRootOpen(false); setNewRootName(''); setCreateRootError(null) }}
      />

      <CreateFolderDialog
        open={createFolder.open}
        parentPath={createFolder.parent}
        rootName={selectedRoot?.name}
        existingNames={createFolder.existingNames}
        loading={createFolder.loading}
        serverError={createFolder.error}
        onConfirm={createFolder.submit}
        onClose={createFolder.close}
      />

      <RenameDialog
        open={rename.open}
        currentName={rename.target?.name ?? ''}
        existingNames={rename.existingNames}
        loading={rename.loading}
        serverError={rename.error}
        onConfirm={rename.submit}
        onClose={rename.close}
      />

      <RenameDialog
        open={syncRootRename.open}
        currentName={syncRootRename.target?.name ?? ''}
        existingNames={syncRootRename.existingNames}
        loading={syncRootRename.loading}
        serverError={syncRootRename.error}
        onConfirm={syncRootRename.submit}
        onClose={syncRootRename.close}
      />

      <CopyDialog
        open={copy.open}
        originalName={copy.originalName}
        defaultName={copy.defaultName}
        existingNames={copy.existingNames}
        loading={copy.loading}
        serverError={copy.error}
        onConfirm={copy.submit}
        onClose={copy.close}
      />

      {ctxMenu && (
        <EntryContextMenu
          anchor={ctxMenu.anchor}
          entries={ctxMenu.entry ? [ctxMenu.entry] : []}
          callbacks={ctxMenu.callbacks}
          onClose={() => setCtxMenu(null)}
        />
      )}

      {/* Self-guards on previews.source — also covers root-list file-mounts. */}
      <FilePreviewLayer
        state={previews}
        actions={previews}
        onEditorShare={
          previews.editor && previews.source?.kind === 'root'
            ? () => goShare((previews.source as { kind: 'root'; rootID: string }).rootID, previews.editor!.path, false)
            : undefined
        }
      />

      <BulkDeleteDialog
        open={bulk.delete.open}
        count={multi.selected.size}
        loading={bulk.delete.loading}
        onConfirm={bulk.delete.confirm}
        onClose={bulk.delete.closeDialog}
      />

      <BulkMoveDialog
        open={bulk.move.open}
        count={multi.selected.size}
        rootID={selectedRoot?.id ?? null}
        rootName={selectedRoot?.name ?? null}
        initialPath={bulk.move.initialPath}
        disabledPaths={new Set(
          Array.from(multi.selected)
            .filter((k) => k.startsWith('dir:'))
            .map((k) => k.slice(4)),
        )}
        loading={bulk.move.loading}
        error={bulk.move.error}
        onConfirm={bulk.move.confirm}
        onClose={bulk.move.closeDialog}
        onFolderCreated={(newPath) => {
          // If the new folder sits in the directory we're showing, refresh
          // so the user sees it back in the main view after closing the dialog.
          if (!selectedRoot) return
          const parent = newPath.slice(0, newPath.lastIndexOf('/')) || '/'
          if (parent === currentPath) loadFiles(selectedRoot.id, currentPath)
          bumpTreeReload()
        }}
      />

      <DeleteEntryDialog
        target={deleteTarget}
        loading={deleteLoading}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />

      <DeleteRootDialog
        target={deleteRootTarget}
        confirm={deleteRootConfirm}
        loading={deleteRootLoading}
        setConfirm={setDeleteRootConfirm}
        onCancel={() => { setDeleteRootTarget(null); setDeleteRootConfirm('') }}
        onConfirm={handleDeleteRoot}
      />

      <MountLeaveDialog
        target={mountLeaveTarget}
        error={mountLeaveError}
        loading={mountLeaveLoading}
        onCancel={() => { setMountLeaveTarget(null); setMountLeaveError(null) }}
        onConfirm={handleLeaveMount}
      />

        </div>
      </div>
    </div>
  )
}
