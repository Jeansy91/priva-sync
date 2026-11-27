import { useTranslation } from '@privasync/i18n'
import type { NavigateFunction } from 'react-router-dom'
import type { SyncRoot, SharedMount } from '@privasync/api-client'
import { FileTree } from './FileTree'
import { toFilesUrl, toMountUrl } from './helpers'
import type { FilesDnd } from './filesViewTypes'

interface FilesPageTreeProps {
  syncRoots:       SyncRoot[]
  mounts:          SharedMount[]
  activeRootID:    string | null
  activePath:      string
  reloadToken:     number
  go:              NavigateFunction

  // Resizable-sidebar handle from useResizableSidebar.
  width:           number
  min:             number
  max:             number
  startResize:     (e: React.MouseEvent) => void
  onKeyDown:       (e: React.KeyboardEvent) => void

  // Drag-and-drop move drop targets for tree nodes.
  dnd?:            Pick<FilesDnd, 'dropProps' | 'isDropActive'>
}

/**
 * Left-hand sync-root sidebar shown on FilesPage. Wraps the FileTree
 * component and the draggable resize separator so the page does not
 * have to spell out the resize a11y attributes inline. Hidden below `lg`:
 * next to the app sidebar it would leave the listing no room, so narrower
 * screens navigate via breadcrumbs and the listing instead.
 */
export function FilesPageTree(props: FilesPageTreeProps) {
  const {
    syncRoots, mounts, activeRootID, activePath, reloadToken, go,
    width, min, max, startResize, onKeyDown, dnd,
  } = props
  const { t } = useTranslation()

  return (
    <>
      <aside
        style={{ width }}
        className="hidden lg:block shrink-0 bg-card overflow-auto"
      >
        <FileTree
          roots={syncRoots}
          mounts={mounts}
          activeRootID={activeRootID}
          activePath={activePath}
          reloadToken={reloadToken}
          dnd={dnd}
          onNavigate={(rootID, path) => {
            // rootID is either an own sync-root id or a mount id; dispatch to
            // the matching URL space (mounts live under the reserved /_m/).
            const root = syncRoots.find((r) => r.id === rootID)
            if (root) { go(toFilesUrl(root.name, path)); return }
            if (mounts.some((m) => m.id === rootID)) go(toMountUrl(rootID, path))
          }}
        />
      </aside>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t('files.treeResize')}
        aria-valuenow={width}
        aria-valuemin={min}
        aria-valuemax={max}
        tabIndex={0}
        onMouseDown={startResize}
        onKeyDown={onKeyDown}
        className="hidden lg:block w-1 shrink-0 cursor-col-resize bg-border hover:bg-primary/40 active:bg-primary/60 focus-visible:bg-primary/60 outline-hidden transition-colors"
      />
    </>
  )
}
