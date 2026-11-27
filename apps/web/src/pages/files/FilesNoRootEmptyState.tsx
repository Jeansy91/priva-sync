import { useTranslation } from '@privasync/i18n'
import { Button } from '@privasync/ui'
import { FolderOpen, FolderPlus } from 'lucide-react'

interface FilesNoRootEmptyStateProps {
  onCreateRoot: () => void
}

/**
 * Centre-aligned placeholder shown at /files when the user has neither
 * own sync roots nor incoming shared mounts. Encourages root creation.
 */
export function FilesNoRootEmptyState({ onCreateRoot }: FilesNoRootEmptyStateProps) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center py-20 gap-4 text-center px-4">
      <FolderOpen className="h-12 w-12 text-muted-foreground" />
      <div>
        <p className="text-sm font-medium">{t('files.noRootTitle')}</p>
        <p className="text-xs text-muted-foreground mt-1">
          {t('files.noRootHint')}
        </p>
      </div>
      <Button onClick={onCreateRoot}>
        <FolderPlus className="h-4 w-4" />
        {t('files.createRoot')}
      </Button>
    </div>
  )
}
