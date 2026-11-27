import { useTranslation } from '@privasync/i18n'
import { Button, cn } from '@privasync/ui'
import { FolderPlus, LayoutGrid, List, PanelRightOpen, PanelRightClose } from 'lucide-react'
import type { SyncRoot } from '@privasync/api-client'
import { FilesSortPicker } from './FilesSortPicker'
import { UploadSplitButton, type UploadSplitButtonHandle, type UploadItem as PickItem } from './UploadSplitButton'
import type { FilesViewMode, FilesViewSort } from './filesViewTypes'

interface FilesActionsBarProps {
  selectedRoot: SyncRoot | null
  mountMode:    boolean
  /** Read-only mounts hide the New-Folder + Upload buttons. */
  activeMountCanWrite: boolean
  onNewFolder:      () => void
  onCreateRoot:     () => void
  uploadBtnRef:     React.RefObject<UploadSplitButtonHandle | null>
  onUploadFiles:    (items: PickItem[]) => void | Promise<void>
  filesSort:        FilesViewSort
  viewMode:         FilesViewMode
  setViewMode:      (m: FilesViewMode) => void
  detailsVisible:   boolean
  setDetailsVisible: (v: boolean | ((prev: boolean) => boolean)) => void
}

/**
 * Toolbar row beneath the breadcrumbs. Hosts the primary action buttons
 * (new folder / upload / new sync root), the view-mode toggle, the sort
 * picker, and the details-panel hide/show switch.
 */
export function FilesActionsBar(props: FilesActionsBarProps) {
  const {
    selectedRoot, mountMode, activeMountCanWrite,
    onNewFolder, onCreateRoot, uploadBtnRef, onUploadFiles,
    filesSort, viewMode, setViewMode,
    detailsVisible, setDetailsVisible,
  } = props
  const { t } = useTranslation()

  return (
    <div className="flex items-center gap-2 border-b border-border bg-background px-4 py-2 shrink-0">
      <div className="flex items-center gap-2 flex-1 min-w-0">
        {selectedRoot && (!mountMode || activeMountCanWrite) ? (
          <>
            <Button
              size="sm"
              onClick={onNewFolder}
              title={t('files.newFolder')}
              aria-label={t('files.newFolder')}
            >
              <FolderPlus className="h-3.5 w-3.5" />
              {/* Icon-only on phones so the view and sort controls still fit. */}
              <span className="hidden sm:inline">{t('files.newFolder')}</span>
            </Button>
            <UploadSplitButton ref={uploadBtnRef} onFiles={onUploadFiles} />
          </>
        ) : !selectedRoot ? (
          <Button size="sm" onClick={onCreateRoot}>
            <FolderPlus className="h-3.5 w-3.5" />
            {t('files.newRoot')}
          </Button>
        ) : null}
      </div>
      <div className="shrink-0 flex items-center gap-0.5 rounded-sm border border-border bg-background p-0.5">
        <button
          type="button"
          onClick={() => setViewMode('table')}
          aria-pressed={viewMode === 'table'}
          aria-label={t('files.viewTable')}
          title={t('files.viewTable')}
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground',
            viewMode === 'table' && 'bg-accent text-foreground',
          )}
        >
          <List className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={() => setViewMode('grid')}
          aria-pressed={viewMode === 'grid'}
          aria-label={t('files.viewGrid')}
          title={t('files.viewGrid')}
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground',
            viewMode === 'grid' && 'bg-accent text-foreground',
          )}
        >
          <LayoutGrid className="h-4 w-4" />
        </button>
      </div>
      <FilesSortPicker sort={filesSort} />
      <button
        type="button"
        onClick={() => setDetailsVisible((v) => !v)}
        aria-pressed={detailsVisible}
        aria-label={detailsVisible ? t('files.hideDetails') : t('files.showDetails')}
        title={detailsVisible ? t('files.hideDetails') : t('files.showDetails')}
        className="hidden lg:inline-flex shrink-0 rounded-sm p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        {detailsVisible ? <PanelRightClose className="h-4 w-4" /> : <PanelRightOpen className="h-4 w-4" />}
      </button>
    </div>
  )
}
