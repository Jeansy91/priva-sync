import { DiffEditor } from '@monaco-editor/react'
import { useTranslation } from '@privasync/i18n'
import { Dialog, DialogContent, Button } from '@privasync/ui'
import { AlertTriangle } from 'lucide-react'
import { monacoTheme } from './monacoTheme'
import type { ConflictState } from './useTextEditorDoc'

// ConflictDiffDialog shows the optimistic-lock clash as a side-by-side diff:
// the server's current content (left, original) against the user's unsaved
// edits (right, modified). The user resolves by keeping their version
// (overwrite) or discarding it for the server's.
export function ConflictDiffDialog({
  conflict, language, busy, onKeepMine, onKeepServer, onDismiss,
}: {
  conflict:     ConflictState
  language:     string
  busy:         boolean
  onKeepMine:   () => void
  onKeepServer: () => void
  onDismiss:    () => void
}) {
  const { t } = useTranslation()

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onDismiss() }}>
      <DialogContent className="max-w-5xl w-[95vw] h-[85vh] p-0 flex flex-col gap-0">
        <div className="flex items-center gap-2 px-4 py-3 border-b border-border shrink-0">
          <AlertTriangle className="h-4 w-4 text-destructive shrink-0" />
          <div>
            <div className="text-sm font-medium">{t('files.editorConflictTitle')}</div>
            <div className="text-xs text-muted-foreground">{t('files.editorConflictDesc')}</div>
          </div>
        </div>
        <div className="flex items-center justify-between px-4 py-1.5 text-xs text-muted-foreground border-b border-border shrink-0">
          <span>{t('files.editorServerLabel')}</span>
          <span>{t('files.editorLocalLabel')}</span>
        </div>
        <div className="flex-1 min-h-0">
          <DiffEditor
            original={conflict.serverContent}
            modified={conflict.localContent}
            language={language}
            theme={monacoTheme()}
            options={{ readOnly: true, renderSideBySide: true, minimap: { enabled: false } }}
          />
        </div>
        <div className="flex justify-end gap-2 px-4 py-3 border-t border-border shrink-0">
          <Button variant="outline" onClick={onKeepServer} disabled={busy}>
            {t('files.editorKeepServer')}
          </Button>
          <Button variant="default" onClick={onKeepMine} disabled={busy}>
            {t('files.editorKeepMine')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
