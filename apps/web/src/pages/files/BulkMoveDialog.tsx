import { useEffect, useState } from 'react'
import { Loader2, Move } from 'lucide-react'
import {
  Button,
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
} from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { FolderTreePicker } from './FolderTreePicker'

type Props = {
  open:    boolean
  count:   number
  rootID:       string | null
  rootName:     string | null
  initialPath:  string
  // Source paths of the move — disabled in the picker so users can't drop
  // a folder onto itself.
  disabledPaths?: Set<string>
  loading: boolean
  error:   string | null
  onConfirm: (targetPath: string) => void
  onClose:   () => void
  onFolderCreated?: (newPath: string) => void
}

export function BulkMoveDialog({
  open, count, rootID, rootName, initialPath, disabledPaths, loading, error,
  onConfirm, onClose, onFolderCreated,
}: Props) {
  const { t } = useTranslation()
  const [path, setPath] = useState(initialPath)
  useEffect(() => { if (open) setPath(initialPath) }, [open, initialPath])

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="w-md max-w-[calc(100vw-2rem)]">
        <DialogHeader>
          <DialogTitle>{t('files.bulkMoveTitle', { count })}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-2">
          <p className="text-sm text-muted-foreground">{t('files.bulkMovePickFolder')}</p>
          {rootID && rootName ? (
            <FolderTreePicker
              rootID={rootID}
              rootName={rootName}
              value={path}
              onChange={setPath}
              disabledPaths={disabledPaths}
              onFolderCreated={onFolderCreated}
            />
          ) : null}
          <p className="text-xs text-muted-foreground break-all">
            {t('files.bulkMoveTargetLabel', { path: (rootName ?? '') + path })}
          </p>
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={loading}>
            {t('common.cancel')}
          </Button>
          <Button onClick={() => onConfirm(path)} disabled={loading || !path}>
            {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Move className="h-4 w-4" />}
            {t('files.bulkMoveSubmit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
